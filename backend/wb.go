package backend

import (
	"context"
	"fmt"
	"net"
	"net/http"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/wbjrunner"
	"github.com/ildarmaga/whitelist-bypass/relay/wbxray"
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

	// Active data-path probe: KCP rtt can stay healthy while the actual TCP
	// forwarding through netstack is dead (traffic trickles at B/s, browser
	// gets ERR_CONNECTION_CLOSED). With full-VPN routes up, the app's own HTTP
	// requests go through the tunnel, so a periodic generate_204 probe tests
	// the real data path. This many consecutive failures = rebuild tunnel.
	wbProbeFailLimit = 3
	wbProbeInterval  = 10 * time.Second
	wbProbeTimeout   = 5 * time.Second

	// Meaningful throughput (not trickle keepalive) — probe ignored only during real load.
	wbMeaningfulRateBps  = 32 * 1024 // 32 KiB/s combined rx+tx
	wbMeaningfulWindow   = 25 * time.Second
	wbTrafficActiveWindow  = 30 * time.Second
	wbTrafficActiveMinDelta = int64(8192)
	wbTrafficStallWindow   = 45 * time.Second

	// Zombie: probe fails while only trickle bytes move (github ERR_CONNECTION_CLOSED).
	wbZombieProbeLimit = 2

	// After carrier rebind the data path can be quiet while KCP settles.
	wbProbeGraceAfterRebind = 90 * time.Second

	wbSoftRecoverMax      = 3
	wbSoftRecoverCooldown = 90 * time.Second
	wbSoftRecoverCooldownDead = 15 * time.Second // RTT dead — retry faster
)

var wbProbeURLs = []string{
	"http://cp.cloudflare.com/generate_204",
	"https://www.gstatic.com/generate_204",
	"https://ya.ru/",
}

// WBManager runs WB Stream in-process (like VK WireGuard), no child wbt-joiner process.
type WBManager struct {
	ctx    context.Context
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	stop   bool
	runGen atomic.Uint64

	room           string
	routingMode    string
	routingPayload string
	vp8Fps         int
	vp8Batch       int
	reconnecting atomic.Bool
	connectedAt  time.Time // when this run started
	sessionStartedAt time.Time // TRAFFIC_READY — uptime for UI
	lastHealthy  time.Time // last stats callback with rtt > 0

	lastStatsLog time.Time
	lastLogRx    int64
	lastLogTx    int64

	recoverCh chan wbjrunner.RecoverRequest

	lastTrafficAt    time.Time
	lastTrafficBytes int64
	lastFastTrafficAt time.Time
	probeGraceUntil  time.Time
	softRecoverCount int
	lastSoftRecover  time.Time
}

func NewWBManager(ctx context.Context) *WBManager {
	return &WBManager{ctx: ctx}
}

func (m *WBManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancel != nil
}

func (m *WBManager) Connect(room string, routingPayload string, vp8Fps, vp8Batch int) error {
	room = strings.TrimSpace(room)
	if room == "" {
		return fmt.Errorf("не задана WB-комната (wb_room) — обновите подписку")
	}
	m.mu.Lock()
	m.stop = false // user explicitly asked to connect
	m.vp8Fps = vp8Fps
	m.vp8Batch = vp8Batch
	m.mu.Unlock()
	return m.connect(room, routingPayload)
}

// connect dials without resetting the user-stop flag, so a user Disconnect
// racing with auto-reconnect always wins.
func (m *WBManager) connect(room, routingPayload string) error {
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
	mode, customRules, err := wbxray.ParseConnectPayload(routingPayload)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("маршрутизация: %w", err)
	}
	m.routingMode = string(mode)
	m.routingPayload = routingPayload
	m.connectedAt = time.Now()
	m.sessionStartedAt = time.Time{}
	m.lastHealthy = time.Time{}
	m.lastStatsLog = time.Time{}
	m.lastLogRx = 0
	m.lastLogTx = 0
	m.lastTrafficAt = time.Time{}
	m.lastTrafficBytes = 0
	m.lastFastTrafficAt = time.Time{}
	m.probeGraceUntil = time.Time{}
	m.softRecoverCount = 0
	m.lastSoftRecover = time.Time{}
	recoverCh := make(chan wbjrunner.RecoverRequest, 1)
	m.recoverCh = recoverCh
	gen := m.runGen.Add(1)
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	m.cancel = cancel
	m.done = done
	vp8Fps, vp8Batch := m.vp8Fps, m.vp8Batch
	m.mu.Unlock()

	if err := prepareWBTun(); err != nil {
		m.finishRun(cancel, done)
		return fmt.Errorf("wintun.dll: %w", err)
	}
	useXray := goruntime.GOOS == "windows"
	var xrayBin string
	if useXray {
		if err := prepareWBXray(); err != nil {
			m.finishRun(cancel, done)
			return fmt.Errorf("xray: %w", err)
		}
		var err error
		xrayBin, err = xrayBinaryPath()
		if err != nil {
			m.finishRun(cancel, done)
			return err
		}
	}

	m.emitLog("INFO", "Подключение WB Stream…")
	runtime.EventsEmit(m.ctx, "state_changed", "connecting")

	go func() {
		defer close(done)
		cfg := wbjrunner.Config{
			Room:              room,
			DisplayName:       "WDTT",
			UseTUN:            true,
			UseXray:           useXray,
			XrayBinary:        xrayBin,
			RoutingMode:       mode,
			CustomRoutingJSON: customRules,
			VP8FPS:            vp8Fps,
			VP8Batch:          vp8Batch,
			RecoverCh:         recoverCh,
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
		runtime.EventsEmit(m.ctx, "tunnel_stats", int64(0), int64(0), int32(0), int32(0), int64(0), float64(0), float64(0), float64(0))
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

// wbTunnelDead is the watchdog decision: rebuild the tunnel when either the
// KCP link reports no healthy rtt for too long, a fresh connect never became
// healthy, or the active HTTP probe through the tunnel failed too many times
// in a row while traffic has actually stalled (rtt can stay alive while the
// data path is dead, but probe alone must not kill an active download).
func wbTunnelDead(now, started, lastHealthy, lastTraffic, lastFast time.Time, lastTrafficBytes int64, probeFails int, probeGraceUntil time.Time) (dead bool, reason string, softRecover bool) {
	if !probeGraceUntil.IsZero() && now.Before(probeGraceUntil) {
		return false, "", false
	}
	meaningful := wbTrafficMeaningful(now, lastFast)
	trafficStalled := wbTrafficStalled(now, lastTraffic, lastTrafficBytes)

	limit := wbProbeFailLimit
	if !meaningful {
		limit = wbZombieProbeLimit
	}
	if probeFails >= limit {
		if meaningful {
			return false, "", false
		}
		if !trafficStalled && !wbTrafficActive(now, lastTraffic, lastTrafficBytes) {
			return false, "", false
		}
		reason = "интернет через туннель не отвечает"
		if wbTrafficActive(now, lastTraffic, lastTrafficBytes) && !meaningful {
			reason = "туннель завис (zombie)"
		}
		return true, reason, true
	}
	if lastHealthy.IsZero() {
		if now.Sub(started) > wbConnectTimeout {
			return true, "туннель не поднялся", false
		}
		return false, "", false
	}
	if now.Sub(lastHealthy) > wbDeadTimeout {
		if meaningful {
			return false, "", false
		}
		return true, "нет живого RTT", true
	}
	return false, "", false
}

func wbTrafficMeaningful(now, lastFast time.Time) bool {
	return !lastFast.IsZero() && now.Sub(lastFast) <= wbMeaningfulWindow
}

func wbTrafficActive(now, lastTraffic time.Time, lastBytes int64) bool {
	if lastTraffic.IsZero() || lastBytes < wbTrafficActiveMinDelta {
		return false
	}
	return now.Sub(lastTraffic) <= wbTrafficActiveWindow
}

func wbTrafficStalled(now, lastTraffic time.Time, lastBytes int64) bool {
	if lastTraffic.IsZero() {
		return true
	}
	if lastBytes < wbTrafficActiveMinDelta {
		return now.Sub(lastTraffic) > wbTrafficStallWindow
	}
	return now.Sub(lastTraffic) > wbTrafficStallWindow
}

// wbProbeDataPath does a real HTTP request; with full-VPN routes up it goes
// through the tunnel and therefore tests actual TCP forwarding, not just KCP.
func wbProbeDataPath() bool {
	client := &http.Client{
		Timeout: wbProbeTimeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			Proxy:             nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				// Match warmup: IPv4-only dial avoids AAAA flake after TUN up.
				ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
				if err != nil || len(ips) == 0 {
					return nil, fmt.Errorf("resolve %s: %w", host, err)
				}
				d := net.Dialer{Timeout: wbProbeTimeout}
				return d.DialContext(ctx, "tcp4", net.JoinHostPort(ips[0].String(), port))
			},
		},
	}
	defer client.CloseIdleConnections()
	for _, u := range wbProbeURLs {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 500 {
			return true
		}
	}
	return false
}

// watchLiveness detects a dead-but-not-exited tunnel and rebuilds it.
func (m *WBManager) watchLiveness(ctx context.Context, gen uint64) {
	t := time.NewTicker(wbWatchTick)
	defer t.Stop()
	probeFails := 0
	lastProbe := time.Now()
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
			lastTraffic := m.lastTrafficAt
			lastTrafficBytes := m.lastTrafficBytes
			lastFast := m.lastFastTrafficAt
			probeGrace := m.probeGraceUntil
			softCount := m.softRecoverCount
			lastSoft := m.lastSoftRecover
			m.mu.Unlock()
			if stale || stopped {
				return
			}

			now := time.Now()
			inGrace := !probeGrace.IsZero() && now.Before(probeGrace)

			// Active probe only after the tunnel was healthy at least once
			// (otherwise we'd count failures during normal connect).
			if !healthy.IsZero() && !inGrace && time.Since(lastProbe) >= wbProbeInterval {
				lastProbe = now
				if wbProbeDataPath() {
					probeFails = 0
				} else {
					if wbTrafficMeaningful(now, lastFast) {
						probeFails = 0
					} else {
						probeFails++
						m.emitLog("WARN", fmt.Sprintf("[WB] Проверка трафика через туннель не прошла (%d/%d)", probeFails, wbZombieProbeLimit))
					}
				}
			}

			dead, reason, trySoft := wbTunnelDead(now, started, healthy, lastTraffic, lastFast, lastTrafficBytes, probeFails, probeGrace)
			if !dead {
				continue
			}

			canSoft := trySoft &&
				softCount < wbSoftRecoverMax &&
				(lastSoft.IsZero() || now.Sub(lastSoft) >= wbSoftRecoverCooldownFor(healthy, now))
			if canSoft {
				// Zombie: KCP-only never restores data path — go straight to session
				// rebind + SwapTunnel (joiner kept, iOS-style bypass auth).
				forceSession := strings.Contains(reason, "zombie") ||
					softCount >= 1 ||
					(!healthy.IsZero() && now.Sub(healthy) > wbDeadTimeout)
				if forceSession {
					m.emitLog("WARN", "[WB] Восстановление WebRTC-сессии без снятия VPN ("+reason+")…")
				} else {
					m.emitLog("WARN", "[WB] Восстановление KCP без снятия VPN ("+reason+")…")
				}
				m.mu.Lock()
				m.softRecoverCount++
				m.lastSoftRecover = now
				m.probeGraceUntil = now.Add(wbProbeGraceAfterRebind)
				m.mu.Unlock()
				probeFails = 0
				m.softRecover(forceSession)
				continue
			}

			m.emitLog("WARN", "[WB] Туннель мёртв ("+reason+") — пересоздаю подключение…")
			runtime.EventsEmit(m.ctx, "state_changed", "connecting")
			m.reconnect(gen)
			return
		}
	}
}

func wbSoftRecoverCooldownFor(lastHealthy, now time.Time) time.Duration {
	if lastHealthy.IsZero() || now.Sub(lastHealthy) > wbDeadTimeout {
		return wbSoftRecoverCooldownDead
	}
	return wbSoftRecoverCooldown
}

func (m *WBManager) softRecover(forceSession bool) {
	m.mu.Lock()
	ch := m.recoverCh
	m.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- wbjrunner.RecoverRequest{ForceSession: forceSession}:
	default:
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
	payload := m.routingPayload
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
	if err := m.connect(room, payload); err != nil {
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
	runtime.EventsEmit(m.ctx, "tunnel_stats", int64(0), int64(0), int32(0), int32(0), int64(0), float64(0), float64(0), float64(0))

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
		m.emitLog("WARN", "[WB] Переподключение WebRTC без снятия VPN…")
		m.mu.Lock()
		m.probeGraceUntil = time.Now().Add(wbProbeGraceAfterRebind)
		m.mu.Unlock()
	case "TRAFFIC_READY":
		m.emitLog("INFO", "[WB] Пробный запрос через туннель успешен")
		m.markSessionStarted()
		runtime.EventsEmit(m.ctx, "state_changed", "running")
		setTrayStatus(true, 0, 0, 1)
	case "TUN_ACTIVE":
		m.emitLog("INFO", "[WB] Полный VPN активен — весь трафик через WB Stream")
	case "WARMUP_FAILED":
		m.emitLog("WARN", "[WB] Пробный запрос не прошёл — проверьте трафик вручную")
		m.markSessionStarted()
		runtime.EventsEmit(m.ctx, "state_changed", "running")
		setTrayStatus(true, 0, 0, 1)
	case "TUN_UNAVAILABLE":
		runtime.EventsEmit(m.ctx, "state_changed", "error")
		m.emitLog("ERROR", "[WB] TUN недоступен — перезапустите WDTT от администратора; проверьте wintun.dll рядом с exe и удалите зависший адаптер WDTT-WB в «Сетевые подключения»")
	}
}

func (m *WBManager) logRelay(raw string) {
	if strings.Contains(raw, "carrier rebind") || strings.Contains(raw, "carrier rebound") {
		m.mu.Lock()
		m.probeGraceUntil = time.Now().Add(wbProbeGraceAfterRebind)
		m.mu.Unlock()
	}
	level, msg, ok := classifyWBLog(raw)
	if !ok {
		return
	}
	m.emitLog(level, msg)
}

func (m *WBManager) markSessionStarted() {
	m.mu.Lock()
	if m.sessionStartedAt.IsZero() {
		m.sessionStartedAt = time.Now()
	}
	m.mu.Unlock()
}

func (m *WBManager) sessionStartedMs() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.sessionStartedAt.IsZero() {
		return m.sessionStartedAt.UnixMilli()
	}
	if !m.connectedAt.IsZero() {
		return m.connectedAt.UnixMilli()
	}
	return 0
}

func (m *WBManager) onStats(rx, tx, rtt, fps int64) {
	startedMs := m.sessionStartedMs()
	runtime.EventsEmit(m.ctx, "tunnel_stats", rx, tx, int32(1), int32(0), startedMs, float64(rtt), float64(fps), float64(rtt))
	setTrayStatus(true, tx, rx, 1)

	m.mu.Lock()
	now := time.Now()
	if rtt > 0 {
		m.lastHealthy = now
	}
	total := rx + tx
	if total > m.lastTrafficBytes+1024 {
		m.lastTrafficAt = now
		m.lastTrafficBytes = total
	}
	shouldLog := now.Sub(m.lastStatsLog) >= wbStatsLogInterval
	if shouldLog {
		prevRx, prevTx := m.lastLogRx, m.lastLogTx
		m.lastStatsLog = now
		m.lastLogRx = rx
		m.lastLogTx = tx
		downRate := float64(rx-prevRx) / wbStatsLogInterval.Seconds()
		upRate := float64(tx-prevTx) / wbStatsLogInterval.Seconds()
		if downRate+upRate >= wbMeaningfulRateBps {
			m.lastFastTrafficAt = now
		}
		m.mu.Unlock()

		if rx == 0 && tx == 0 {
			return
		}
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
