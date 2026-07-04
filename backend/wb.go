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
	// Teardown of the previous WB tunnel (gVisor drain + wintun route removal)
	// can take well over 5s when the link stalled with active flows. Starting a
	// new connection before the old adapter's split-default routes are gone
	// sends guest-register auth into the dead tunnel → TLS handshake timeout on
	// every reconnect. Wait long enough for a full serial teardown (iOS-style).
	wbShutdownWait = 25 * time.Second

	// Liveness: healthy stats report rtt > 0 (~90-140ms). After "tunnel lost"
	// the runner keeps emitting stats with rtt == 0 while its internal
	// re-register loop hits the dead netstack (guest-register EOF, backoff up
	// to 32s+). If we see no healthy rtt for this long, the in-place recovery
	// has failed — tear everything down and reconnect from scratch (iOS-style).
	wbDeadTimeout = 30 * time.Second
	// If a fresh connect never becomes healthy at all, retry from scratch too.
	wbConnectTimeout = 90 * time.Second
	wbWatchTick      = 5 * time.Second
	wbReconnectDelay = 2 * time.Second
)

// WBManager runs WB Stream in-process (like VK WireGuard), no child wbt-joiner process.
type WBManager struct {
	ctx    context.Context
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	stop   bool
	runGen atomic.Uint64

	room         string
	reconnecting atomic.Bool
	connectedAt  time.Time // when this run started
	lastHealthy  time.Time // last stats callback with rtt > 0

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
	m.mu.Lock()
	m.stop = false // user explicitly asked to connect
	m.mu.Unlock()
	return m.connect(room)
}

// connect dials without resetting the user-stop flag, so a user Disconnect
// racing with auto-reconnect always wins.
func (m *WBManager) connect(room string) error {
	m.awaitShutdown(wbShutdownWait)

	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return fmt.Errorf("WB туннель уже запущен")
	}
	if m.stop {
		m.mu.Unlock()
		return fmt.Errorf("подключение отменено")
	}
	m.room = room
	m.connectedAt = time.Now()
	m.lastHealthy = time.Time{}
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
		if stopped {
			return
		}
		// Tunnel died without user action — rebuild it from scratch.
		m.emitLog("WARN", "[WB] Туннель завершился — пересоздаю подключение…")
		runtime.EventsEmit(m.ctx, "state_changed", "connecting")
		go m.reconnect(gen)
	}()

	go m.watchLiveness(ctx, gen)
	return nil
}

// watchLiveness detects a dead-but-not-exited tunnel: after "tunnel lost" the
// runner's internal recovery re-registers through its own (dead) netstack and
// backs off forever, while stats keep coming with rtt == 0. When no healthy
// rtt is seen for wbDeadTimeout, force a full teardown + reconnect.
func (m *WBManager) watchLiveness(ctx context.Context, gen uint64) {
	t := time.NewTicker(wbWatchTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			stale := m.runGen.Load() != gen
			stopped := m.stop
			healthy := m.lastHealthy
			started := m.connectedAt
			m.mu.Unlock()
			if stale || stopped {
				return
			}
			var dead bool
			if !healthy.IsZero() {
				dead = time.Since(healthy) > wbDeadTimeout
			} else {
				dead = time.Since(started) > wbConnectTimeout
			}
			if dead {
				m.emitLog("WARN", "[WB] Туннель мёртв (нет живого RTT) — пересоздаю подключение…")
				runtime.EventsEmit(m.ctx, "state_changed", "connecting")
				m.reconnect(gen)
				return
			}
		}
	}
}

// reconnect tears the current run down (if any) and dials again with the same
// room. Deduplicated: run-exit handler and liveness watchdog may both fire.
func (m *WBManager) reconnect(gen uint64) {
	if !m.reconnecting.CompareAndSwap(false, true) {
		return
	}
	defer m.reconnecting.Store(false)

	m.mu.Lock()
	if m.runGen.Load() != gen || m.stop {
		m.mu.Unlock()
		return
	}
	room := m.room
	cancel := m.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel() // stop the zombie run; Connect below waits for full teardown
	}
	time.Sleep(wbReconnectDelay)

	m.mu.Lock()
	stopped := m.stop
	m.mu.Unlock()
	if stopped {
		return
	}

	m.emitLog("INFO", "[WB] Переподключение к новой сессии…")
	if err := m.connect(room); err != nil {
		m.mu.Lock()
		stopped = m.stop
		m.mu.Unlock()
		if stopped {
			return
		}
		m.emitLog("ERROR", "[WB] Реконнект не удался: "+err.Error())
		time.Sleep(5 * time.Second)
		m.mu.Lock()
		stopped = m.stop
		cur := m.runGen.Load()
		m.mu.Unlock()
		if !stopped {
			go m.reconnect(cur)
		}
	}
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
	// Always set stop: auto-reconnect may be in-flight with cancel == nil,
	// and the user's disconnect must abort it.
	m.stop = true
	cancel := m.cancel
	m.mu.Unlock()

	// UI must not block on gVisor/WebRTC teardown (can take 10–20s with active flows).
	runtime.EventsEmit(m.ctx, "state_changed", "stopped")
	setTrayStatus(false, 0, 0, 0)
	runtime.EventsEmit(m.ctx, "tunnel_stats", int64(0), int64(0), int32(0), float64(0), float64(0), float64(0))

	if cancel != nil {
		cancel()
	}
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
	if rtt > 0 {
		m.lastHealthy = now
	}
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
