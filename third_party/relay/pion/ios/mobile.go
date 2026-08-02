package ios

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
	"github.com/ildarmaga/whitelist-bypass/relay/pion"
	joiner "github.com/ildarmaga/whitelist-bypass/relay/pion/headless-joiner-common"
	"github.com/ildarmaga/whitelist-bypass/relay/tunnel"
	"github.com/ildarmaga/whitelist-bypass/relay/vkcore"
	"github.com/ildarmaga/whitelist-bypass/relay/vkwg"
	"github.com/ildarmaga/whitelist-bypass/relay/wbtunnel"
)

// iOS never ran an active liveness watchdog (desktop-only, see
// pwdtt-restore backend/wb.go watchLiveness/RecoverCh): a WBT carrier only
// rebinds on iOS when Pion itself re-fires OnConnected (ICE renegotiation
// inside the same session). If ICE reports "connected" while KCP/smux is
// actually dead (zombie — the exact "ICE disconnected → connected repeatedly"
// flapping desktop self-heals from), nothing on iOS ever notices or acts,
// so the tunnel just sits dead until the user force-reconnects manually.
// This mirrors the first (cheapest) rung of desktop's escalation ladder:
// KCP+smux resync (RestartLink) without touching WebRTC/SOCKS — same call
// SwapTunnel already uses reactively, just triggered proactively here.
const (
	wbtWatchdogTick         = 5 * time.Second
	wbtWatchdogDeadTimeout  = 20 * time.Second
	wbtWatchdogSoftMax      = 3
	wbtWatchdogSoftCooldown = 12 * time.Second
)

// watchWBTLiveness proactively detects a zombie WBT tunnel (KCP/RTT dead and
// no data-path traffic) and soft-recovers KCP+smux in place, without tearing
// down the WebRTC session or the SOCKS listener. It exits once j is replaced
// or the headless session stops.
func watchWBTLiveness(j *wbtunnel.Joiner, logFn func(string, ...any)) {
	t := time.NewTicker(wbtWatchdogTick)
	defer t.Stop()

	var lastHealthy, lastTraffic, lastSoft time.Time
	var lastBytes uint64
	var softCount int

	for range t.C {
		activeHeadless.Lock()
		stillActive := !activeHeadless.stopped && activeHeadless.wbtJoin == j
		activeHeadless.Unlock()
		if !stillActive {
			return
		}

		now := time.Now()
		if rtt := j.RTTMs(); rtt > 0 {
			lastHealthy = now
		}
		rx, tx, _ := sessionstats.Snapshot()
		if total := rx + tx; total > lastBytes {
			lastBytes = total
			lastTraffic = now
		}
		if lastHealthy.IsZero() || lastTraffic.IsZero() {
			continue // still warming up after connect/rebind
		}

		rttDead := now.Sub(lastHealthy) > wbtWatchdogDeadTimeout
		trafficDead := now.Sub(lastTraffic) > wbtWatchdogDeadTimeout
		if !rttDead && !trafficDead {
			if softCount > 0 {
				softCount = 0 // tunnel proved itself healthy again
			}
			continue
		}
		// After a burst of soft recovers, cool down 60s then try another round
		// (do not kill the NE session — Swift ERROR: would stop the tunnel).
		if softCount >= wbtWatchdogSoftMax {
			if now.Sub(lastSoft) < 60*time.Second {
				continue
			}
			softCount = 0
		}
		if !lastSoft.IsZero() && now.Sub(lastSoft) < wbtWatchdogSoftCooldown {
			continue
		}

		softCount++
		lastSoft = now
		logFn("wbt: watchdog — no RTT/traffic for %v, KCP+smux soft recover (attempt %d/%d)", wbtWatchdogDeadTimeout, softCount, wbtWatchdogSoftMax)
		if err := j.RestartLink(true); err != nil {
			logFn("wbt: watchdog RestartLink failed: %v", err)
			continue
		}
		// Fresh traffic baseline after RestartLink.
		lastHealthy = now
		rx, tx, _ = sessionstats.Snapshot()
		lastBytes = rx + tx
		lastTraffic = now
	}
}

type HeadlessCallback interface {
	OnLog(msg string)
	OnStatus(status string)
	ResolveHost(hostname string) string
	SaveCache(key string, value string)
	LoadCache(key string) string
	ClearCache(key string)
}

type joinerHandle interface {
	Close()
}

var activeHeadless struct {
	sync.Mutex
	joiner   joinerHandle
	callback HeadlessCallback
	socksLn  net.Listener
	bridge   *tunnel.RelayBridge
	wbtJoin  *wbtunnel.Joiner
	vkwg     *vkwg.Bridge
	stopped  bool
	platform string
}

type iosStatusEmitter struct {
	statusFn func(string)
}

func (e *iosStatusEmitter) EmitStatus(status string)   { e.statusFn(status) }
func (e *iosStatusEmitter) EmitStatusError(msg string) { e.statusFn("ERROR:" + msg) }

type iosCacheStore struct {
	callback HeadlessCallback
}

func (c *iosCacheStore) Save(key string, value string) { c.callback.SaveCache(key, value) }
func (c *iosCacheStore) Load(key string) string        { return c.callback.LoadCache(key) }

func makeOnConnectedWBT(socksPort int, socksUser, socksPass string, logFn func(string, ...any), callback HeadlessCallback) func(tunnel.DataTunnel) {
	return func(tun tunnel.DataTunnel) {
		activeHeadless.Lock()
		if activeHeadless.stopped {
			activeHeadless.Unlock()
			return
		}
		existing := activeHeadless.wbtJoin
		activeHeadless.Unlock()

		if existing != nil {
			if err := existing.SwapTunnel(tun, nil); err != nil {
				logFn("wbt: swap tunnel: %v", err)
			} else {
				logFn("wbt: tunnel swapped, keeping SOCKS sessions")
			}
			return
		}

		j, err := wbtunnel.NewJoiner(context.Background(), tun, socksUser, socksPass, logFn, nil)
		if err != nil {
			logFn("wbt: joiner init: %v", err)
			callback.OnStatus("ERROR:wbt init: " + err.Error())
			return
		}

		activeHeadless.Lock()
		if activeHeadless.stopped {
			activeHeadless.Unlock()
			j.Close()
			return
		}
		activeHeadless.wbtJoin = j
		activeHeadless.Unlock()

		go watchWBTLiveness(j, logFn)

		socksAddr := fmt.Sprintf("%s:%d", common.SocksListenAll, socksPort)
		logFn("wbt: SOCKS5 on %s (KCP+smux over VP8)", socksAddr)
		go func() {
			if err := j.ListenSOCKS(socksAddr); err != nil {
				logFn("wbt: SOCKS listen: %v", err)
				callback.OnStatus("ERROR:socks listen: " + err.Error())
			}
		}()
	}
}

func makeOnConnected(socksPort int, socksUser, socksPass string, logFn func(string, ...any), callback HeadlessCallback) func(tunnel.DataTunnel) {
	return func(tun tunnel.DataTunnel) {
		activeHeadless.Lock()
		if activeHeadless.stopped {
			activeHeadless.Unlock()
			return
		}
		existing := activeHeadless.bridge
		activeHeadless.Unlock()

		if existing != nil {
			existing.SwapTunnelKeepConns(tun, false)
			logFn("ios: tunnel swapped, keeping SOCKS sessions")
			return
		}

		// Large SOCKS read buffer for full-device VPN (v2ray → WLB); VP8 coalesces on send.
		readBuf := common.DCBufSize
		bridge := tunnel.NewRelayBridgeWithAuth(tun, "joiner", readBuf, logFn, socksUser, socksPass)
		bridge.SetPersistentListener(true)
		bridge.MarkReady()

		activeHeadless.Lock()
		if activeHeadless.stopped {
			activeHeadless.Unlock()
			bridge.Close()
			return
		}
		activeHeadless.bridge = bridge
		activeHeadless.Unlock()

		// 0.0.0.0: VPN clients (v2ray/Streisand NE) often cannot reach 127.0.0.1
		// from the tunnel; binding all interfaces allows 127.0.0.1 or LAN IP.
		socksAddr := fmt.Sprintf("%s:%d", common.SocksListenAll, socksPort)
		logFn("ios: SOCKS5 proxy starting on %s (use 127.0.0.1:%d or device LAN IP in v2ray)", socksAddr, socksPort)
		go func() {
			if err := bridge.ListenSOCKS(socksAddr); err != nil {
				logFn("ios: SOCKS5 listen error: %v", err)
				callback.OnStatus("ERROR:socks listen: " + err.Error())
			}
		}()
	}
}

func makeHelpers(callback HeadlessCallback) (func(string, ...any), joiner.ResolveFunc, *iosStatusEmitter) {
	logFn := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		callback.OnLog(msg)
	}
	resolveFn := func(hostname string) (string, error) {
		result := callback.ResolveHost(hostname)
		if result == "" {
			return "", fmt.Errorf("empty resolve for %s", hostname)
		}
		return result, nil
	}
	statusEmitter := &iosStatusEmitter{
		statusFn: func(status string) {
			callback.OnStatus(status)
		},
	}
	return logFn, resolveFn, statusEmitter
}

func init() {
}

func StartWBStreamHeadless(socksPort int, socksUser, socksPass string, callback HeadlessCallback) {
	StopHeadless()

	activeHeadless.Lock()
	activeHeadless.callback = callback
	activeHeadless.stopped = false
	activeHeadless.platform = "wbstream"
	activeHeadless.Unlock()

	logFn, resolveFn, statusEmitter := makeHelpers(callback)
	wbJoiner := joiner.NewWBStreamHeadlessJoiner(logFn, resolveFn, statusEmitter, nil)
	wbJoiner.OnConnected = makeOnConnectedWBT(socksPort, socksUser, socksPass, logFn, callback)

	activeHeadless.Lock()
	activeHeadless.joiner = wbJoiner
	activeHeadless.Unlock()

	callback.OnStatus(common.StatusReady)
}

func StartDionHeadless(socksPort int, socksUser, socksPass string, callback HeadlessCallback) {
	StopHeadless()

	activeHeadless.Lock()
	activeHeadless.callback = callback
	activeHeadless.stopped = false
	activeHeadless.platform = "dion"
	activeHeadless.Unlock()

	logFn, resolveFn, statusEmitter := makeHelpers(callback)
	dionJoiner := joiner.NewDionHeadlessJoiner(logFn, resolveFn, statusEmitter, nil)
	dionJoiner.OnConnected = makeOnConnected(socksPort, socksUser, socksPass, logFn, callback)

	activeHeadless.Lock()
	activeHeadless.joiner = dionJoiner
	activeHeadless.Unlock()

	callback.OnStatus(common.StatusReady)
}

func StartTelemostHeadless(socksPort int, socksUser, socksPass string, callback HeadlessCallback) {
	StopHeadless()

	activeHeadless.Lock()
	activeHeadless.callback = callback
	activeHeadless.stopped = false
	activeHeadless.platform = "telemost"
	activeHeadless.Unlock()

	logFn, resolveFn, statusEmitter := makeHelpers(callback)
	tmJoiner := joiner.NewTelemostHeadlessJoiner(logFn, resolveFn, statusEmitter, nil, pion.AddTunnelTracks, pion.ReadTrack)
	tmJoiner.OnConnected = makeOnConnected(socksPort, socksUser, socksPass, logFn, callback)

	activeHeadless.Lock()
	activeHeadless.joiner = tmJoiner
	activeHeadless.Unlock()

	callback.OnStatus(common.StatusReady)
}

func StartVKHeadless(socksPort int, socksUser, socksPass string, joinLink, displayName, tunnelMode string, vp8Fps, vp8Batch int, dualTrack bool, callback HeadlessCallback) {
	StopHeadless()

	activeHeadless.Lock()
	activeHeadless.callback = callback
	activeHeadless.stopped = false
	activeHeadless.platform = "vk"
	activeHeadless.Unlock()

	logFn, resolveFn, statusEmitter := makeHelpers(callback)
	vkJoiner := joiner.NewVKHeadlessJoiner(logFn, resolveFn, statusEmitter, nil, pion.AddTunnelTracks, pion.ReadTrack)
	vkJoiner.OnConnected = makeOnConnected(socksPort, socksUser, socksPass, logFn, callback)

	activeHeadless.Lock()
	activeHeadless.joiner = vkJoiner
	activeHeadless.Unlock()

	go func() {
		authJSON, err := joiner.RunVKAuth(joinLink, displayName, logFn, statusEmitter.statusFn, &iosCacheStore{callback: callback}, resolveFn)
		if err != nil {
			logFn("vk-auth: failed: %v", err)
			callback.OnStatus("ERROR:" + err.Error())
			return
		}
		var params map[string]interface{}
		if json.Unmarshal([]byte(authJSON), &params) == nil {
			params["tunnelMode"] = tunnelMode
			if vp8Fps > 0 {
				params["vp8Fps"] = vp8Fps
			}
			if vp8Batch > 0 {
				params["vp8Batch"] = vp8Batch
			}
			params["dualTrack"] = dualTrack
			if patched, err := json.Marshal(params); err == nil {
				authJSON = string(patched)
			}
		}
		logFn("vk-auth: sending join params to relay mode=%s vp8Fps=%d vp8Batch=%d dualTrack=%v", tunnelMode, vp8Fps, vp8Batch, dualTrack)
		vkJoiner.RunWithParams(authJSON)
	}()
}

// StartVKWireGuardHeadless connects VK using the PC-style WireGuard+TURN
// protocol (ip/dtls/pass/hash from the subscription) entirely in userspace
// (netstack) and exposes a local SOCKS5 proxy. hashesCSV is the comma-separated
// "hash" field from the wdtt link.
func StartVKWireGuardHeadless(socksPort int, socksUser, socksPass string, peerAddr, password, hashesCSV, deviceID string, workers, mtu int, callback HeadlessCallback) {
	StopHeadless()

	activeHeadless.Lock()
	activeHeadless.callback = callback
	activeHeadless.stopped = false
	activeHeadless.platform = "vkwg"
	activeHeadless.Unlock()

	logFn, _, statusEmitter := makeHelpers(callback)

	var hashes []string
	for _, h := range strings.Split(hashesCSV, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hashes = append(hashes, h)
		}
	}

	socksAddr := fmt.Sprintf("%s:%d", common.SocksListenAll, socksPort)
	b := vkwg.New(vkwg.Config{
		PeerAddr:    peerAddr,
		Password:    password,
		Hashes:      hashes,
		DeviceID:    deviceID,
		Workers:     workers,
		MTU:         mtu,
		CaptchaMode: "auto",
		SocksListen: socksAddr,
		SocksUser:   socksUser,
		SocksPass:   socksPass,
		Log:         func(level, msg string) { logFn("[%s] %s", level, msg) },
	})

	activeHeadless.Lock()
	activeHeadless.vkwg = b
	activeHeadless.Unlock()

	logFn("vk-wg: connecting peer=%s hashes=%d socks=%s", peerAddr, len(hashes), socksAddr)
	callback.OnStatus(common.StatusReady)

	go func() {
		if err := b.Start(); err != nil {
			logFn("vk-wg: start failed: %v", err)
			statusEmitter.statusFn("ERROR:" + err.Error())
			return
		}
		statusEmitter.statusFn(common.StatusTunnelConnected)
	}()
}

func SendJoinParams(jsonParams string) {
	activeHeadless.Lock()
	currentJoiner := activeHeadless.joiner
	platform := activeHeadless.platform
	activeHeadless.Unlock()

	if currentJoiner == nil {
		return
	}

	switch platform {
	case "telemost":
		if tmJoiner, ok := currentJoiner.(*joiner.TelemostHeadlessJoiner); ok {
			go tmJoiner.RunWithParams(jsonParams)
		}
	case "vk":
		if vkJoiner, ok := currentJoiner.(*joiner.VKHeadlessJoiner); ok {
			go vkJoiner.RunWithParams(jsonParams)
		}
	case "wbstream":
		if wbJoiner, ok := currentJoiner.(*joiner.WBStreamHeadlessJoiner); ok {
			go wbJoiner.RunWithParams(jsonParams)
		}
	case "dion":
		if dionJoiner, ok := currentJoiner.(*joiner.DionHeadlessJoiner); ok {
			go dionJoiner.RunWithParams(jsonParams)
		}
	}
}

func StopCaptchaProxy() {
	joiner.StopCaptchaProxy()
}

// ConfigureVKCookies applies the VK cookie-auth toggle and optional cookie
// payload from the iOS settings UI before connect. Returns JSON
// {"ok":bool,"hint":"..."} or {"error":"..."}.
func ConfigureVKCookies(useCookies bool, rawCookies string) string {
	if err := vkcore.ConfigureVKAuth(useCookies, rawCookies); err != nil {
		b, _ := json.Marshal(map[string]any{"error": err.Error()})
		return string(b)
	}
	ok, hint := vkcore.VKCookiesStatus()
	b, _ := json.Marshal(map[string]any{"ok": ok, "hint": hint})
	return string(b)
}

// VKCookiesStatusJSON returns live cookie validation for the settings UI.
func VKCookiesStatusJSON() string {
	ok, hint := vkcore.VKCookiesStatus()
	use := vkcore.VKUseCookies()
	expired := use && !ok && hint == vkcore.VKCookieExpiredHint()
	b, _ := json.Marshal(map[string]any{
		"ok":         ok,
		"hint":       hint,
		"useCookies": use,
		"expired":    expired,
	})
	return string(b)
}

func StopHeadless() {
	activeHeadless.Lock()
	activeHeadless.stopped = true
	currentJoiner := activeHeadless.joiner
	socksLn := activeHeadless.socksLn
	bridge := activeHeadless.bridge
	wbtJoin := activeHeadless.wbtJoin
	vkwgBridge := activeHeadless.vkwg
	activeHeadless.joiner = nil
	activeHeadless.socksLn = nil
	activeHeadless.bridge = nil
	activeHeadless.wbtJoin = nil
	activeHeadless.vkwg = nil
	activeHeadless.callback = nil
	activeHeadless.platform = ""
	activeHeadless.Unlock()

	sessionstats.Reset()

	joiner.StopCaptchaProxy()
	if currentJoiner != nil {
		currentJoiner.Close()
	}
	if bridge != nil {
		bridge.Close()
	}
	if wbtJoin != nil {
		wbtJoin.Close()
	}
	if vkwgBridge != nil {
		vkwgBridge.Stop()
	}
	if socksLn != nil {
		socksLn.Close()
	}
}

// SessionStatsJSON returns SOCKS session counters for the iOS UI.
func SessionStatsJSON() string {
	rx, tx, rtt := sessionstats.Snapshot()
	activeHeadless.Lock()
	wbtJoin := activeHeadless.wbtJoin
	vkwgBridge := activeHeadless.vkwg
	activeHeadless.Unlock()
	if wbtJoin != nil {
		if live := wbtJoin.RTTMs(); live > 0 {
			rtt = live
			sessionstats.SetRTT(live)
		}
	}
	if vkwgBridge != nil {
		snap := vkwgBridge.Snapshot()
		rx, tx = uint64(snap.RxBytes), uint64(snap.TxBytes)
		if snap.TurnRTTMs > 0 {
			rtt = int(snap.TurnRTTMs)
		}
		payload, _ := json.Marshal(map[string]any{
			"rx":              rx,
			"tx":              tx,
			"rtt":             rtt,
			"workers":         snap.Workers,
			"assignedWorkers": snap.AssignedWorkers,
			"dtls":            int(snap.DTLSHSMs),
		})
		return string(payload)
	}
	payload, _ := json.Marshal(map[string]any{
		"rx":  rx,
		"tx":  tx,
		"rtt": rtt,
	})
	return string(payload)
}
