package backend

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ildarmaga/whitelist-bypass/relay/wbjrunner"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// WBManager runs WB Stream in-process (like VK WireGuard), no child wbt-joiner process.
type WBManager struct {
	ctx    context.Context
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	stop   bool
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
	if m.cancel != nil {
		m.mu.Unlock()
		return fmt.Errorf("WB туннель уже запущен")
	}
	m.stop = false
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
		stopped := m.stop
		m.cancel = nil
		m.mu.Unlock()
		if !stopped {
			m.emitLog("WARN", "WB туннель завершился")
		}
		m.emitLog("INFO", "— Отключено")
		runtime.EventsEmit(m.ctx, "state_changed", "stopped")
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
	m.stop = true
	cancel := m.cancel
	done := m.done
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (m *WBManager) onStatus(code string) {
	switch code {
	case "TUNNEL_CONNECTED":
		m.emitLog("INFO", "[WB] WebRTC готов · поднимаю VPN…")
	case "TRAFFIC_READY":
		m.emitLog("INFO", "[WB] Пробный запрос через туннель успешен")
		runtime.EventsEmit(m.ctx, "state_changed", "running")
	case "TUN_ACTIVE":
		m.emitLog("INFO", "[WB] Полный VPN активен — весь трафик через WB Stream")
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
