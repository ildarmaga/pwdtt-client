package backend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/wbjrunner"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	wbStatsLogInterval = 3 * time.Second
	wbShutdownWait     = 5 * time.Second
)

// WBManager runs WB Stream in-process (like VK WireGuard), no child wbt-joiner process.
type WBManager struct {
	ctx    context.Context
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	stop   bool
	runGen atomic.Uint64

	lastStatsLog time.Time
	lastLogRx    int64
	lastLogTx    int64
}

func NewWBManager(ctx context.Context) *WBManager {
	return &WBManager{ctx: ctx}
}

func (m *WBManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancel != nil
}

func (m *WBManager) Connect(room string) error {
	room = strings.TrimSpace(room)
	if room == "" {
		return fmt.Errorf("не задана WB-комната (wb_room) — обновите подписку")
	}
	m.awaitShutdown(wbShutdownWait)

	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return fmt.Errorf("WB туннель уже запущен")
	}
	m.stop = false
	m.lastStatsLog = time.Time{}
	m.lastLogRx = 0
	m.lastLogTx = 0
	gen := m.runGen.Add(1)
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	m.cancel = cancel
	m.done = done
	m.mu.Unlock()

	if err := prepareWBTun(); err != nil {
		m.finishRun(cancel, done)
		return fmt.Errorf("wintun.dll: %w", err)
	}

	m.emitLog("INFO", "Подключение WB Stream…")
	runtime.EventsEmit(m.ctx, "state_changed", "connecting")

	go func() {
		defer close(done)
		cfg := wbjrunner.Config{
			Room:        room,
			DisplayName: "WDTT",
			UseTUN:      true,
			LogFn: func(format string, args ...any) {
				m.logRelay(fmt.Sprintf(format, args...))
			},
			OnStatus: m.onStatus,
			OnStats:  m.onStats,
		}
		_ = wbjrunner.Run(ctx, cfg)

		m.mu.Lock()
		stale := m.runGen.Load() != gen
		stopped := m.stop
		if !stale {
			m.cancel = nil
		}
		m.mu.Unlock()

		if stale {
			return
		}

		setTrayStatus(false, 0, 0, 0)
		runtime.EventsEmit(m.ctx, "tunnel_stats", int64(0), int64(0), int32(0), float64(0), float64(0), float64(0))
		if !stopped {
			m.emitLog("WARN", "WB туннель завершился")
			m.emitLog("INFO", "— Отключено")
			runtime.EventsEmit(m.ctx, "state_changed", "stopped")
		}
	}()
	return nil
}

func (m *WBManager) finishRun(cancel context.CancelFunc, done chan struct{}) {
	cancel()
	<-done
	m.mu.Lock()
	m.cancel = nil
	m.mu.Unlock()
}

func (m *WBManager) Disconnect() {
	m.mu.Lock()
	if m.cancel == nil {
		m.mu.Unlock()
		return
	}
	m.stop = true
	cancel := m.cancel
	m.mu.Unlock()

	// UI must not block on gVisor/WebRTC teardown (can take 10–20s with active flows).
	runtime.EventsEmit(m.ctx, "state_changed", "stopped")
	setTrayStatus(false, 0, 0, 0)
	runtime.EventsEmit(m.ctx, "tunnel_stats", int64(0), int64(0), int32(0), float64(0), float64(0), float64(0))

	cancel()
}

// awaitShutdown waits for the runner goroutine to exit after Disconnect (or crash).
func (m *WBManager) awaitShutdown(max time.Duration) {
	m.mu.Lock()
	done := m.done
	if m.cancel == nil {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	if done == nil {
		emergencyStopWBTun()
		m.mu.Lock()
		m.cancel = nil
		m.mu.Unlock()
		return
	}

	deadline := time.After(max)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-done:
			m.mu.Lock()
			m.cancel = nil
			m.done = nil
			m.mu.Unlock()
			return
		case <-deadline:
			m.emitLog("WARN", "WB: принудительная остановка туннеля")
			emergencyStopWBTun()
			m.mu.Lock()
			m.cancel = nil
			m.done = nil
			m.mu.Unlock()
			return
		case <-tick.C:
			m.emitLog("INFO", "[WB] Ожидание завершения предыдущего подключения…")
		}
	}
}

func (m *WBManager) onStatus(code string) {
	m.mu.Lock()
	stopping := m.stop
	m.mu.Unlock()
	if stopping {
		return
	}
	switch code {
	case "TUNNEL_CONNECTED":
		m.emitLog("INFO", "[WB] WebRTC готов · поднимаю VPN…")
	case "TUNNEL_RECONNECTING":
		m.emitLog("WARN", "[WB] Переподключение туннеля…")
		runtime.EventsEmit(m.ctx, "state_changed", "connecting")
	case "TRAFFIC_READY":
		m.emitLog("INFO", "[WB] Пробный запрос через туннель успешен")
		runtime.EventsEmit(m.ctx, "state_changed", "running")
		setTrayStatus(true, 0, 0, 1)
	case "TUN_ACTIVE":
		m.emitLog("INFO", "[WB] Полный VPN активен — весь трафик через WB Stream")
	case "WARMUP_FAILED":
		m.emitLog("WARN", "[WB] Пробный запрос не прошёл — проверьте трафик вручную")
		runtime.EventsEmit(m.ctx, "state_changed", "running")
		setTrayStatus(true, 0, 0, 1)
	case "TUN_UNAVAILABLE":
		runtime.EventsEmit(m.ctx, "state_changed", "error")
		m.emitLog("ERROR", "[WB] TUN недоступен — запустите WDTT от администратора")
	}
}

func (m *WBManager) logRelay(raw string) {
	level, msg, ok := classifyWBLog(raw)
	if !ok {
		return
	}
	m.emitLog(level, msg)
}

func (m *WBManager) onStats(rx, tx, rtt, fps int64) {
	runtime.EventsEmit(m.ctx, "tunnel_stats", rx, tx, 1, rtt, fps, rtt)
	setTrayStatus(true, tx, rx, 1)

	m.mu.Lock()
	now := time.Now()
	shouldLog := now.Sub(m.lastStatsLog) >= wbStatsLogInterval
	if shouldLog {
		prevRx, prevTx := m.lastLogRx, m.lastLogTx
		m.lastStatsLog = now
		m.lastLogRx = rx
		m.lastLogTx = tx
		m.mu.Unlock()

		if rx == 0 && tx == 0 {
			return
		}
		dt := wbStatsLogInterval.Seconds()
		downRate := float64(rx-prevRx) / dt
		upRate := float64(tx-prevTx) / dt
		totalMB := float64(rx+tx) / (1024.0 * 1024.0)
		m.emitLog("INFO", fmt.Sprintf(
			"[WB СТАТ] ↓ %s (%s) ↑ %s (%s) · WBT %d ms · %.2f MB",
			formatWBBytes(rx), formatWBRate(downRate),
			formatWBBytes(tx), formatWBRate(upRate),
			rtt, totalMB,
		))
		return
	}
	m.mu.Unlock()
}

func formatWBBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024.0
	v := float64(n)
	if v < unit {
		return fmt.Sprintf("%d B", n)
	}
	if v < unit*unit {
		return fmt.Sprintf("%.1f KB", v/unit)
	}
	if v < unit*unit*unit {
		return fmt.Sprintf("%.2f MB", v/(unit*unit))
	}
	return fmt.Sprintf("%.2f GB", v/(unit*unit*unit))
}

func formatWBRate(bps float64) string {
	if bps <= 0 {
		return "0 B/s"
	}
	const unit = 1024.0
	if bps < unit {
		return fmt.Sprintf("%.0f B/s", bps)
	}
	if bps < unit*unit {
		return fmt.Sprintf("%.1f KB/s", bps/unit)
	}
	return fmt.Sprintf("%.1f MB/s", bps/(unit*unit))
}

func (m *WBManager) emitLog(level, msg string) {
	runtime.EventsEmit(m.ctx, "log", level, msg)
}

func maskRoom(room string) string {
	id := room
	if i := strings.LastIndex(room, "/"); i >= 0 {
		id = room[i+1:]
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
