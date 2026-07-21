package backend

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"net"
	"net/http"
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
	// Teardown of the previous WB tunnel. SOCKS relays are force-closed on
	// Joiner.Close (2s cap); keep a short wait so reconnect does not race the
	// old Run() goroutine, without the old 25s hang under v2rayN keepalives.
	wbShutdownWait = 8 * time.Second

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
	wbDownloadStallWindow = 35 * time.Second // rx frozen while probe fails → zombie

	// Zombie: probe fails while download stalled (upload trickle ≠ healthy).
	wbZombieProbeLimit = 3

	// After carrier rebind the data path can be quiet while KCP settles.
	wbProbeGraceAfterRebind = 90 * time.Second
	// SOCKS/v2rayN: no system probe possible — short grace then require real traffic
	// (or OpenStream burst → full reconnect). 90s left users stuck at 0 B/s.
	wbSocksProbeGraceAfterRebind = 20 * time.Second
	wbRecoverVerifyWait          = 25 * time.Second // soft recover must pass probe by then or full reconnect
	wbSocksRecoverVerifyWait     = 20 * time.Second

	wbSoftRecoverMax      = 3
	wbSoftRecoverCooldown = 90 * time.Second
	wbSoftRecoverCooldownDead = 15 * time.Second // RTT dead — retry faster

	// OpenStream/closed pipe flood after soft rebound → full reconnect (SOCKS).
	wbTunnelErrBurstRecover = 12
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
	vp8DualTrack   bool
	socksOnly      bool
	socksHost      string
	socksPort      int
	socksUser      string
	socksPass      string
	socksReady     bool
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
	lastRxAt         time.Time
	lastRxBytes      int64
	lastFastTrafficAt time.Time
	probeGraceUntil  time.Time
	recoverVerifyUntil time.Time
	softRecoverCount int
	lastSoftRecover  time.Time
	lastRTT          int64 // ms — adaptive probe interval on mobile
	tunnelErrBurst   int
	lastTunnelErrLog time.Time
}

func NewWBManager(ctx context.Context) *WBManager {
	return &WBManager{ctx: ctx}
}

func (m *WBManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	// User Disconnect sets stop=true; teardown may take up to wbShutdownWait but
	// updates/install must be allowed immediately after UI shows "idle".
	if m.cancel == nil || m.stop {
		return false
	}
	return true
}

func (m *WBManager) Connect(room string, routingPayload string, vp8Fps, vp8Batch int, dualTrack bool, socksOnly bool, socksPort int, socksUser, socksPass string) error {
	room = strings.TrimSpace(room)
	if room == "" {
		return fmt.Errorf("не задана WB-комната (wb_room) — обновите подписку")
	}
	// Built-in TUN/xray disabled: WB is SOCKS-only (v2rayN/V2BOX), like iOS.
	_ = socksOnly
	socksOnly = true
	if socksPort <= 0 {
		socksPort = 10809
	}
	m.mu.Lock()
	m.stop = false // user explicitly asked to connect
	m.vp8Fps = vp8Fps
	m.vp8Batch = vp8Batch
	m.vp8DualTrack = dualTrack
	m.socksOnly = true
	m.socksHost = "127.0.0.1"
	m.socksPort = socksPort
	m.socksUser = strings.TrimSpace(socksUser)
	m.socksPass = socksPass
	m.socksReady = false
	if m.socksUser == "" {
		m.socksUser, m.socksPass = genSocksCreds()
	}
	m.mu.Unlock()
	return m.connect(room, routingPayload)
}

// SocksEndpoint returns the local SOCKS5 address after SOCKS_READY (socks-only mode).
func (m *WBManager) SocksEndpoint() (host string, port int, user, pass string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.socksReady || m.socksPort <= 0 {
		return "", 0, "", "", false
	}
	return m.socksHost, m.socksPort, m.socksUser, m.socksPass, true
}

func genSocksCreds() (user, pass string) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 40)
	if _, err := crand.Read(b); err != nil {
		return "wdtt", "wdtt"
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:16]), string(b[16:])
}

// connect dials without resetting the user-stop flag, so a user Disconnect
// racing with auto-reconnect always wins.
func (m *WBManager) connect(room, routingPayload string) error {
	m.awaitShutdown(wbShutdownWait)

	m.mu.Lock()
	if m.cancel != nil {
		// Already up (UI desync / double-click). Re-emit connected so frontend recovers.
		socksReady := m.socksReady
		host, port, user, pass := m.socksHost, m.socksPort, m.socksUser, m.socksPass
		m.mu.Unlock()
		m.emitLog("INFO", "[WB] туннель уже активен — синхронизирую UI")
		if socksReady && host != "" && port > 0 {
			runtime.EventsEmit(m.ctx, "wb_socks_ready", host, port, user, pass)
			runtime.EventsEmit(m.ctx, "state_changed", "running")
		} else {
			runtime.EventsEmit(m.ctx, "state_changed", "connecting")
		}
		return nil
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
	m.lastRxAt = time.Time{}
	m.lastRxBytes = 0
	m.lastFastTrafficAt = time.Time{}
	m.probeGraceUntil = time.Time{}
	m.recoverVerifyUntil = time.Time{}
	m.softRecoverCount = 0
	m.lastSoftRecover = time.Time{}
	m.tunnelErrBurst = 0
	m.lastTunnelErrLog = time.Time{}
	recoverCh := make(chan wbjrunner.RecoverRequest, 1)
	m.recoverCh = recoverCh
	gen := m.runGen.Add(1)
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	m.cancel = cancel
	m.done = done
	vp8Fps, vp8Batch := m.vp8Fps, m.vp8Batch
	vp8DualTrack := m.vp8DualTrack
	socksHost := m.socksHost
	socksPort := m.socksPort
	socksUser := m.socksUser
	socksPass := m.socksPass
	m.mu.Unlock()

	m.emitLog("INFO", "Подключение WB Stream…")
	runtime.EventsEmit(m.ctx, "state_changed", "connecting")

	go func() {
		defer close(done)
		cfg := wbjrunner.Config{
			Room:              room,
			DisplayName:       "WDTT",
			UseTUN:            false,
			UseXray:           false,
			SocksOnly:         true,
			SocksHost:         socksHost,
			SocksPort:         socksPort,
			SocksUser:         socksUser,
			SocksPass:         socksPass,
			RoutingMode:       mode,
			CustomRoutingJSON: customRules,
			VP8FPS:            vp8Fps,
			VP8Batch:          vp8Batch,
			DualTrack:         vp8DualTrack,
			RecoverCh:         recoverCh,
			LogFn: func(format string, args ...any) {
				m.logRelay(fmt.Sprintf(format, args...))
			},
			OnStatus: m.onStatus,
			OnStats:  m.onStats,
			OnSocksReady: func(host string, port int, user, pass string) {
				m.mu.Lock()
				m.socksHost = host
				m.socksPort = port
				m.socksUser = user
				m.socksPass = pass
				m.socksReady = true
				m.mu.Unlock()
				runtime.EventsEmit(m.ctx, "wb_socks_ready", host, port, user, pass)
			},
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
		// ctx canceled = deliberate teardown (Disconnect or reconnect), not a
		// crash. A quick user re-Connect resets stop=false before this old run
		// finishes exiting — without this check the handler would spawn a
		// second dial racing the user's one (reconnect storm).
		if ctx.Err() != nil {
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
func wbTunnelDead(now, started, lastHealthy, lastTraffic, lastFast, lastRxAt time.Time, lastTrafficBytes int64, probeFails int, probeGraceUntil time.Time) (dead bool, reason string, softRecover bool) {
	if !probeGraceUntil.IsZero() && now.Before(probeGraceUntil) {
		return false, "", false
	}
	meaningful := wbTrafficMeaningful(now, lastFast)
	downloadStalled := wbDownloadStalled(now, lastRxAt)

	limit := wbProbeFailLimit
	if !meaningful {
		limit = wbZombieProbeLimit
	}
	if probeFails >= limit {
		if meaningful {
			return false, "", false
		}
		// Do not zombie-kill while download is still moving.
		if !downloadStalled {
			return false, "", false
		}
		reason = "интернет через туннель не отвечает"
		if wbTrafficActive(now, lastTraffic, lastTrafficBytes) {
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

func wbDownloadStalled(now, lastRxAt time.Time) bool {
	if lastRxAt.IsZero() {
		return true
	}
	return now.Sub(lastRxAt) > wbDownloadStallWindow
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
			lastRxAt := m.lastRxAt
			lastFast := m.lastFastTrafficAt
			probeGrace := m.probeGraceUntil
			verifyUntil := m.recoverVerifyUntil
			softCount := m.softRecoverCount
			lastSoft := m.lastSoftRecover
			lastRTT := m.lastRTT
			m.mu.Unlock()
			if stale || stopped {
				return
			}

			now := time.Now()
			inGrace := !probeGrace.IsZero() && now.Before(probeGrace)

			// Soft recover (WebRTC rebind) must restore real TCP within N seconds.
			if !verifyUntil.IsZero() && now.After(verifyUntil) {
				m.mu.Lock()
				m.recoverVerifyUntil = time.Time{}
				socksVerify := m.socksOnly
				trafficAt := m.lastTrafficAt
				m.mu.Unlock()
				ok := false
				if socksVerify {
					// Probe would go direct (no system routes). Require byte progress.
					ok = !trafficAt.IsZero() && now.Sub(trafficAt) <= 15*time.Second
				} else {
					ok = wbProbeDataPath()
				}
				if ok {
					m.mu.Lock()
					m.softRecoverCount = 0
					m.mu.Unlock()
					probeFails = 0
				} else {
					m.emitLog("WARN", "[WB] Восстановление не помогло — полное переподключение…")
					runtime.EventsEmit(m.ctx, "state_changed", "connecting")
					m.reconnect(gen)
					return
				}
			}

			// Active probe only after the tunnel was healthy at least once
			// (otherwise we'd count failures during normal connect).
			probeInterval := wbProbeIntervalForRTT(lastRTT)
			skipProbe := false
			m.mu.Lock()
			rm := m.routingMode
			socks := m.socksOnly
			m.mu.Unlock()
			if socks {
				skipProbe = true // no system routes — probe would go direct, not through SOCKS
			}
			switch rm {
			case "custom", "ru_direct", "bypass_lan":
				skipProbe = true
			}
			if !healthy.IsZero() && !inGrace && !skipProbe && time.Since(lastProbe) >= probeInterval {
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

			dead, reason, trySoft := wbTunnelDead(now, started, healthy, lastTraffic, lastFast, lastRxAt, lastTrafficBytes, probeFails, probeGrace)
			if !dead {
				continue
			}
			// SOCKS/WBT: RTT floor stays >0 while smux is dead — do not kill on
			// RTT alone. Traffic stall after grace → full reconnect (soft already tried).
			if socks {
				if reason == "нет живого RTT" {
					continue
				}
				if reason == "туннель не поднялся" && !lastTraffic.IsZero() &&
					now.Sub(lastTraffic) <= wbDeadTimeout {
					continue
				}
				if !inGrace && wbTrafficStalled(now, lastTraffic, lastTrafficBytes) {
					m.emitLog("WARN", "[WB] SOCKS туннель без трафика — полное переподключение…")
					runtime.EventsEmit(m.ctx, "state_changed", "connecting")
					m.reconnect(gen)
					return
				}
			}

			canSoft := trySoft &&
				softCount < wbSoftRecoverMax &&
				(lastSoft.IsZero() || now.Sub(lastSoft) >= wbSoftRecoverCooldownFor(healthy, now))
			if canSoft {
				// Zombie: KCP-only never restores data path — go straight to session
				// rebind + SwapTunnel (joiner kept, iOS-style bypass auth).
				// SOCKS: always ForceSession (KCP-only RestartLink is useless alone).
				forceSession := socks ||
					strings.Contains(reason, "zombie") ||
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
				if socks {
					m.probeGraceUntil = now.Add(wbSocksProbeGraceAfterRebind)
					m.recoverVerifyUntil = now.Add(wbSocksRecoverVerifyWait)
				} else {
					m.probeGraceUntil = now.Add(wbProbeGraceAfterRebind)
					if forceSession {
						m.recoverVerifyUntil = now.Add(wbRecoverVerifyWait)
					}
				}
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

// wbProbeIntervalForRTT — on high RTT mobile, probe less often to avoid false zombie detection.
func wbProbeIntervalForRTT(rttMs int64) time.Duration {
	switch {
	case rttMs > 400:
		return 22 * time.Second
	case rttMs > 250:
		return 18 * time.Second
	case rttMs > 150:
		return 14 * time.Second
	default:
		return wbProbeInterval
	}
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
		active := m.cancel != nil // a user Connect won the race — leave it alone
		cur := m.runGen.Load()
		m.mu.Unlock()
		if !stopped && !active {
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
	m.socksReady = false
	cancel := m.cancel
	// Capture the run being stopped under the same lock: if the user quickly
	// reconnects, m.done/m.runGen will already belong to the NEW run by the
	// time the watcher goroutine below starts — waiting on those would
	// emergency-stop a healthy fresh tunnel at the deadline.
	done := m.done
	gen := m.runGen.Load()
	m.mu.Unlock()

	// UI must not block on gVisor/WebRTC teardown (can take 10–20s with active flows).
	runtime.EventsEmit(m.ctx, "state_changed", "stopped")
	runtime.EventsEmit(m.ctx, "wb_socks_ready", "", 0, "", "")
	setTrayStatus(false, 0, 0, 0)
	runtime.EventsEmit(m.ctx, "tunnel_stats", int64(0), int64(0), int32(0), int32(0), int64(0), float64(0), float64(0), float64(0))

	if cancel != nil {
		cancel()
		go m.awaitShutdownRun(done, gen, wbShutdownWait)
	} else {
		go emergencyStopWBTun()
	}
}

// awaitShutdown waits for the current runner goroutine to exit (used before
// dialing a new session).
func (m *WBManager) awaitShutdown(max time.Duration) {
	m.mu.Lock()
	if m.cancel == nil {
		m.mu.Unlock()
		return
	}
	done := m.done
	gen := m.runGen.Load()
	m.mu.Unlock()
	m.awaitShutdownRun(done, gen, max)
}

// awaitShutdownRun waits for a specific run (identified by its done channel and
// generation) to exit. All teardown actions are generation-scoped: once a newer
// run exists, this watcher must not touch shared TUN state — emergencyStopWBTun
// is global and would kill the new tunnel.
func (m *WBManager) awaitShutdownRun(done chan struct{}, gen uint64, max time.Duration) {
	if done == nil {
		if m.runGen.Load() == gen {
			emergencyStopWBTun()
			m.clearRun(gen)
		}
		return
	}

	deadline := time.After(max)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-done:
			m.clearRun(gen)
			return
		case <-deadline:
			if m.runGen.Load() != gen {
				return // superseded by a newer run — never emergency-stop it
			}
			m.emitLog("WARN", "WB: принудительная остановка туннеля")
			emergencyStopWBTun()
			m.clearRun(gen)
			return
		case <-tick.C:
			if m.runGen.Load() != gen {
				return
			}
			m.emitLog("INFO", "[WB] Ожидание завершения предыдущего подключения…")
		}
	}
}

// clearRun resets cancel/done only if gen is still the active generation.
func (m *WBManager) clearRun(gen uint64) {
	m.mu.Lock()
	if m.runGen.Load() == gen {
		m.cancel = nil
		m.done = nil
	}
	m.mu.Unlock()
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
		m.mu.Lock()
		socks := m.socksOnly
		m.mu.Unlock()
		if socks {
			m.emitLog("INFO", "[WB] WebRTC готов · жду ICE rebind, затем SOCKS…")
		} else {
			m.emitLog("INFO", "[WB] WebRTC готов · жду ICE rebind, затем VPN…")
		}
	case "TUNNEL_RECONNECTING":
		m.emitLog("WARN", "[WB] Переподключение WebRTC без снятия VPN…")
		m.mu.Lock()
		if m.socksOnly {
			m.probeGraceUntil = time.Now().Add(wbSocksProbeGraceAfterRebind)
			m.recoverVerifyUntil = time.Now().Add(wbSocksRecoverVerifyWait)
		} else {
			m.probeGraceUntil = time.Now().Add(wbProbeGraceAfterRebind)
			m.recoverVerifyUntil = time.Now().Add(wbRecoverVerifyWait)
		}
		m.mu.Unlock()
	case "TRAFFIC_READY":
		m.emitLog("INFO", "[WB] Пробный запрос через туннель успешен")
		m.markConnectedUI()
	case "SOCKS_READY":
		m.mu.Lock()
		host, port, user, pass := m.socksHost, m.socksPort, m.socksUser, m.socksPass
		// WBT RTT may still be 0 when SOCKS first binds — mark healthy so the
		// watchdog does not kill after wbConnectTimeout ("туннель не поднялся").
		m.lastHealthy = time.Now()
		m.mu.Unlock()
		m.emitLog("INFO", fmt.Sprintf("[WB] SOCKS5 готов — 127.0.0.1:%d (user=%s)", port, user))
		if host != "" && port > 0 {
			runtime.EventsEmit(m.ctx, "wb_socks_ready", host, port, user, pass)
		}
		m.markConnectedUI()
	case "SOCKS_UNAVAILABLE":
		runtime.EventsEmit(m.ctx, "state_changed", "error")
		m.emitLog("ERROR", "[WB] Не удалось открыть SOCKS-порт — смените порт в настройках")
	case "TUN_ACTIVE":
		m.emitLog("INFO", "[WB] Полный VPN активен — весь трафик через WB Stream")
		m.markConnectedUI()
	case "WARMUP_FAILED":
		m.emitLog("WARN", "[WB] Пробный запрос не прошёл — проверьте трафик вручную")
		m.markConnectedUI()
	case "TUN_UNAVAILABLE":
		runtime.EventsEmit(m.ctx, "state_changed", "error")
		m.emitLog("ERROR", "[WB] TUN недоступен — перезапустите WDTT от администратора; проверьте wintun.dll рядом с exe и удалите зависший адаптер WDTT-WB в «Сетевые подключения»")
	}
}

func (m *WBManager) logRelay(raw string) {
	if strings.Contains(raw, "carrier rebind") || strings.Contains(raw, "carrier rebound") {
		m.mu.Lock()
		if m.socksOnly {
			m.probeGraceUntil = time.Now().Add(wbSocksProbeGraceAfterRebind)
			m.recoverVerifyUntil = time.Now().Add(wbSocksRecoverVerifyWait)
		} else {
			m.probeGraceUntil = time.Now().Add(wbProbeGraceAfterRebind)
		}
		m.mu.Unlock()
	}
	if strings.Contains(raw, "OpenStream:") || strings.Contains(raw, "remote not ready") {
		m.mu.Lock()
		now := time.Now()
		if now.Sub(m.lastTunnelErrLog) > 5*time.Second {
			m.tunnelErrBurst = 0
		}
		m.tunnelErrBurst++
		m.lastTunnelErrLog = now
		burst := m.tunnelErrBurst
		gen := m.runGen.Load()
		stopped := m.stop
		m.mu.Unlock()
		// Soft rebound left smux dead: floor RTT stays "healthy", SOCKS shows -1.
		// Escalate to full reconnect instead of flooding logs forever.
		if burst == wbTunnelErrBurstRecover && !stopped && !m.reconnecting.Load() {
			m.emitLog("WARN", "[WB] OpenStream/smux мёртв после rebind — полное переподключение…")
			runtime.EventsEmit(m.ctx, "state_changed", "connecting")
			go m.reconnect(gen)
			return
		}
		if burst > 5 {
			return
		}
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

// markConnectedUI switches the frontend to "connected" once VPN routes are up.
// Warmup ipify runs in the background and no longer blocks the UI.
func (m *WBManager) markConnectedUI() {
	m.mu.Lock()
	already := !m.sessionStartedAt.IsZero()
	if !already {
		m.sessionStartedAt = time.Now()
	}
	m.mu.Unlock()
	if already {
		return
	}
	runtime.EventsEmit(m.ctx, "state_changed", "running")
	setTrayStatus(true, 0, 0, 1)
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
	socks := m.socksOnly
	if rtt > 0 {
		m.lastHealthy = now
		m.lastRTT = rtt
	}
	total := rx + tx
	if rx > m.lastRxBytes+1024 {
		m.lastRxAt = now
		m.lastRxBytes = rx
	}
	if total > m.lastTrafficBytes+1024 {
		m.lastTrafficAt = now
		m.lastTrafficBytes = total
		// SOCKS/WBT: traffic proves the data path while RTT is still settling.
		if socks && rtt <= 0 {
			m.lastHealthy = now
		}
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
