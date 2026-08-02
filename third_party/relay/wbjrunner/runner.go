package wbjrunner

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
	"github.com/ildarmaga/whitelist-bypass/relay/desktoptun"
	"github.com/ildarmaga/whitelist-bypass/relay/tunnel"
	"github.com/ildarmaga/whitelist-bypass/relay/wbstream"
	"github.com/ildarmaga/whitelist-bypass/relay/wbtunnel"
	"github.com/ildarmaga/whitelist-bypass/relay/wbxray"
	"github.com/pion/webrtc/v4"
)

const (
	tunAdapterBase = "WDTT-WB"
	tunIP          = "10.99.0.2"
	tunMask        = "255.255.255.0"
	tunPeer        = "10.99.0.1"
	tunMTU         = 1380 // match WG; WebRTC+KCP overhead on path to VPS
)

// newTunAdapterName returns a unique per-run wintun adapter name. xray creates a
// wintun device keyed by name; reusing a fixed name collides with a lingering
// pool from a previous/crashed session (CreateAdapter → 0x800700B7). A fresh
// name always creates cleanly. Detection/config is by ifIndex + device
// description, so the concrete name does not matter for routing.
func newTunAdapterName() string {
	var b [4]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%08x", tunAdapterBase, time.Now().UnixNano()&0xffffffff)
	}
	return fmt.Sprintf("%s-%s", tunAdapterBase, hex.EncodeToString(b[:]))
}

// Config drives an in-process WB Stream joiner (desktop VPN or e2e probe).
type Config struct {
	Room        string
	DisplayName string
	VP8FPS      int
	VP8Batch    int
	DualTrack   bool   // second VP8 ScreenShare track (SFU look); KCP stays on camera
	UseTUN      bool   // full VPN via wintun (WDTT desktop)
	UseXray     bool   // xray TUN + SOCKS joiner (replaces gVisor netstack)
	XrayBinary  string // path to xray.exe; required when UseXray
	RoutingMode wbxray.RoutingMode
	CustomRoutingJSON string
	E2EProbe    bool   // dialer probe only, no OS TUN (headless tests)
	// SocksOnly — iOS-style: WebRTC/KCP joiner + local SOCKS5 only (no wintun/xray).
	// Point v2rayN / V2BOX / Clash at 127.0.0.1:SocksPort for system VPN.
	SocksOnly bool
	SocksHost string // default 127.0.0.1
	SocksPort int    // default 10808; 0 = ephemeral
	SocksUser string
	SocksPass string
	DNSServers []string

	LogFn    func(format string, args ...any)
	OnStatus func(code string) // TUNNEL_CONNECTED, TUN_ACTIVE, SOCKS_READY, TRAFFIC_READY, …
	OnStats  func(rx, tx, rtt, fps int64)
	// OnSocksReady reports the bound SOCKS endpoint after ServeSOCKS starts (SocksOnly).
	OnSocksReady func(host string, port int, user, pass string)

	// RecoverCh triggers in-place WebRTC/KCP recovery without tearing down the
	// OS TUN adapter (split routes stay up — no internet drop).
	RecoverCh <-chan RecoverRequest
}

// RecoverRequest triggers in-place tunnel recovery without tearing down the OS TUN.
type RecoverRequest struct {
	// ForceSession closes the WebRTC session (not just KCP+smux) when KCP-only
	// recovery did not restore the data path.
	ForceSession bool
}

// Run connects to WB Stream and blocks until ctx is cancelled. Returns ctx.Err()
// on normal shutdown.
func Run(ctx context.Context, cfg Config) error {
	if cfg.LogFn == nil {
		cfg.LogFn = func(string, ...any) {}
	}
	restoreTimer := enableHighResTimer()
	defer restoreTimer()
	if cfg.DisplayName == "" {
		cfg.DisplayName = "Joiner"
	}
	if len(cfg.DNSServers) == 0 && !cfg.UseTUN {
		cfg.DNSServers = []string{"1.1.1.1", "8.8.8.8"}
	}

	// SocksOnly = WBT/KCP + local SOCKS5 (v2rayN), same transport as pre-xray
	// gVisor path — NOT RelayBridge. RelayBridge (no KCP) caused ERR_SSL /
	// dead ↓ on Windows; dual-track only works with KCP SendRaw reorder.
	fps, batch := cfg.VP8FPS, cfg.VP8Batch
	if fps <= 0 {
		fps = 30
	}
	if batch <= 0 {
		batch = 64
	}
	cfg.LogFn("[wbt] vp8 pacing fps=%d batch=%d dualTrack=%v", fps, batch, cfg.DualTrack)

	room := strings.TrimSpace(cfg.Room)
	if room == "" {
		return fmt.Errorf("room is required")
	}
	if !cfg.UseTUN && !cfg.E2EProbe && !cfg.SocksOnly {
		return fmt.Errorf("UseTUN, SocksOnly, or E2EProbe is required")
	}
	if cfg.SocksOnly && cfg.UseTUN {
		return fmt.Errorf("SocksOnly and UseTUN are mutually exclusive")
	}

	if cfg.UseTUN && cfg.UseXray && strings.TrimSpace(cfg.XrayBinary) == "" {
		return fmt.Errorf("XrayBinary is required when UseXray is set")
	}
	if cfg.SocksOnly {
		if cfg.SocksHost == "" {
			cfg.SocksHost = common.SocksLocalhostIP
		}
		if cfg.SocksPort < 0 {
			cfg.SocksPort = 0
		}
	}

	sessionstats.Reset()

	tunAdapter := newTunAdapterName()

	roomID := wbstream.ParseRoomID(room)
	dialer := &lazyJoinerDialer{}

	var tun *desktoptun.Tunnel
	var routeShell *desktoptun.RouteShell
	var bypass *tunBypass
	if cfg.UseTUN {
		if cfg.UseXray {
			rs, err := desktoptun.NewRouteShell(tunAdapter, cfg.LogFn)
			if err != nil {
				return fmt.Errorf("route shell: %w", err)
			}
			routeShell = rs
			bypass = newTunBypass(routeShell, cfg.LogFn)
		} else {
			tunCfg := desktoptun.Config{
				AdapterName: tunAdapter,
				TunnelIP:    tunIP,
				TunnelMask:  tunMask,
				TunnelPeer:  tunPeer,
				MTU:         tunMTU,
				DNSServers:  cfg.DNSServers, // nil = VK-style: system DNS stays on physical NIC
				Dialer:      dialer,
				LogFn:       cfg.LogFn,
			}
			t, err := desktoptun.New(tunCfg)
			if err != nil {
				return fmt.Errorf("tun init: %w", err)
			}
			tun = t
			bypass = newTunBypass(tun, cfg.LogFn)
		}
	}

	var settingEngine *webrtc.SettingEngine
	if cfg.UseTUN {
		se := webrtc.SettingEngine{}
		se.SetIPFilter(func(ip net.IP) bool {
			if v4 := ip.To4(); v4 != nil {
				// Never bind WebRTC to tunnel or link-local ghost wintun addresses.
				if v4[0] == 10 && v4[1] == 99 {
					return false
				}
				if v4[0] == 169 && v4[1] == 254 {
					return false
				}
			}
			return true
		})
		settingEngine = &se
	}

	var (
		mu              sync.Mutex
		joiner          *wbtunnel.Joiner
		activeSess      *wbstream.Session
		tunOnce         sync.Once
		tunUp           atomic.Bool
		joinLoopWG      sync.WaitGroup
		xrayRunner      *wbxray.Runner
		directEgressIP  string
		// setupGate serializes VPN bring-up with shutdown: tearing the adapter
		// down while xray is mid-launch leaves a live xray + split routes for
		// the next run (reconnect storm, smux desync).
		setupGate = make(chan struct{}, 1)
		socksOnce       sync.Once
		socksLn         net.Listener
		// carrierRebound signals first WebRTC carrier SwapTunnel after join.
		// Buffered so a rebound before startSocksOnly/bringUpVPN starts waiting
		// is not lost (deferred sub-offer often lands within ~1s).
		carrierRebound = make(chan struct{}, 1)
	)

	emitStatus := func(code string) {
		if ctx.Err() != nil {
			return
		}
		if cfg.OnStatus != nil {
			cfg.OnStatus(code)
		}
	}

	signalCarrierRebound := func() {
		select {
		case carrierRebound <- struct{}{}:
		default:
		}
	}

	// waitCarrierSettle waits briefly for ICE renegotiation / SwapTunnel.
	// Deferred second rebind is skipped now — first connect almost never gets a
	// rebound, so maxWait must stay short (was 5s → SOCKS felt "slow").
	waitCarrierSettle := func(label string, maxWait, afterRebound time.Duration) (ok bool, gotRebound bool) {
		t0 := time.Now()
		cfg.LogFn("[wbt] %s: waiting for carrier rebound (max %v)…", label, maxWait)
		select {
		case <-ctx.Done():
			cfg.LogFn("[wbt] %s: cancelled after %v", label, time.Since(t0).Round(time.Millisecond))
			return false, false
		case <-carrierRebound:
			cfg.LogFn("[wbt] %s: carrier rebound after %v — short settle %v", label, time.Since(t0).Round(time.Millisecond), afterRebound)
			select {
			case <-ctx.Done():
				return false, true
			case <-time.After(afterRebound):
			}
			cfg.LogFn("[wbt] %s: settle done (total %v)", label, time.Since(t0).Round(time.Millisecond))
			return true, true
		case <-time.After(maxWait):
			cfg.LogFn("[wbt] %s: no rebound in %v — first-connect path (elapsed %v)", label, maxWait, time.Since(t0).Round(time.Millisecond))
			return true, false
		}
	}

	startSocksOnly := func(j *wbtunnel.Joiner) {
		socksOnce.Do(func() {
			tSocks := time.Now()
			cfg.LogFn("[wbt] SOCKS bring-up start")
			ok, gotRebound := waitCarrierSettle("pre-SOCKS", 800*time.Millisecond, 300*time.Millisecond)
			if !ok {
				return
			}
			select {
			case setupGate <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-setupGate }()
			// RestartLink only if SwapTunnel already ran — otherwise we just
			// rebuilt smux for no reason and burned dial-hold time.
			if gotRebound {
				cfg.LogFn("[wbt] pre-SOCKS RestartLink (KCP+smux sync)…")
				tSync := time.Now()
				if err := j.RestartLink(true); err != nil {
					cfg.LogFn("[wbt] pre-SOCKS KCP sync: %v (after %v)", err, time.Since(tSync).Round(time.Millisecond))
				} else {
					cfg.LogFn("[wbt] pre-SOCKS KCP+smux synced in %v", time.Since(tSync).Round(time.Millisecond))
				}
			} else {
				cfg.LogFn("[wbt] pre-SOCKS: skip RestartLink (carrier already live)")
			}
			if ctx.Err() != nil {
				return
			}
			port := cfg.SocksPort
			addr := net.JoinHostPort(cfg.SocksHost, strconv.Itoa(port))
			cfg.LogFn("[wbt] SOCKS listen %s…", addr)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				cfg.LogFn("[socks] listen %s: %v", addr, err)
				emitStatus("SOCKS_UNAVAILABLE")
				return
			}
			mu.Lock()
			socksLn = ln
			mu.Unlock()
			_, portStr, _ := net.SplitHostPort(ln.Addr().String())
			boundPort, _ := strconv.Atoi(portStr)
			go func() { _ = j.ServeSOCKS(ln) }()
			cfg.LogFn("wbt: SOCKS5 on %s (total bring-up %v)", ln.Addr().String(), time.Since(tSocks).Round(time.Millisecond))
			if cfg.OnSocksReady != nil {
				cfg.OnSocksReady(cfg.SocksHost, boundPort, cfg.SocksUser, cfg.SocksPass)
			}
			emitStatus("SOCKS_READY")
		})
	}

	runWarmup := func() {
		if cfg.UseXray {
			go warmupXrayVPN(cfg.LogFn, emitStatus, directEgressIP)
			return
		}
		go warmupTunnel(dialer, cfg.LogFn, emitStatus)
	}

	bringUpVPN := func() {
		if tunUp.Load() {
			runWarmup()
			return
		}
		tunOnce.Do(func() {
			cfg.LogFn("[wbt] VPN bring-up start")
			ok, gotRebound := waitCarrierSettle("pre-VPN", 800*time.Millisecond, 300*time.Millisecond)
			if !ok {
				return
			}
			select {
			case setupGate <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-setupGate }()
			mu.Lock()
			j := joiner
			mu.Unlock()
			if j != nil && gotRebound {
				cfg.LogFn("[wbt] pre-VPN RestartLink (KCP+smux sync)…")
				tSync := time.Now()
				if err := j.RestartLink(true); err != nil {
					cfg.LogFn("[wbt] pre-VPN KCP sync: %v (after %v)", err, time.Since(tSync).Round(time.Millisecond))
				} else {
					cfg.LogFn("[wbt] pre-VPN KCP+smux synced in %v", time.Since(tSync).Round(time.Millisecond))
				}
			} else if j != nil {
				cfg.LogFn("[wbt] pre-VPN: skip RestartLink (carrier already live)")
			}
			if err := tunPrecheck(); err != nil {
				cfg.LogFn("[tun] недоступен: %v", err)
				emitStatus("TUN_UNAVAILABLE")
				return
			}

			if cfg.UseXray {
				mu.Lock()
				j := joiner
				mu.Unlock()
				if j == nil {
					cfg.LogFn("[xray] joiner not ready")
					emitStatus("TUN_UNAVAILABLE")
					return
				}
				ln, err := net.Listen("tcp", net.JoinHostPort(common.SocksLocalhostIP, "0"))
				if err != nil {
					cfg.LogFn("[xray] socks listen: %v", err)
					emitStatus("TUN_UNAVAILABLE")
					return
				}
				_, portStr, _ := net.SplitHostPort(ln.Addr().String())
				socksPort, _ := strconv.Atoi(portStr)
				go func() { _ = j.ServeSOCKS(ln) }()

				if err := routeShell.Prepare(); err != nil {
					cfg.LogFn("[xray] route shell: %v", err)
					ln.Close()
					emitStatus("TUN_UNAVAILABLE")
					return
				}
				egressAlias, egressIP, egressIdx := routeShell.EgressIface()
				if egressIP == "" {
					if egressAlias != "" {
						cfg.LogFn("[xray] warn: no IPv4 on %q — direct via auto", egressAlias)
					}
					egressAlias = ""
				} else {
					cfg.LogFn("[xray] direct egress via %q ip=%s ifIndex=%d", egressAlias, egressIP, egressIdx)
				}
				if ip, err := probeOSRouteEgress(); err == nil && ip != "" {
					directEgressIP = ip
					cfg.LogFn("[xray] direct egress ip=%s (pre-tun)", directEgressIP)
				}
				prepXray := func(deep bool) {
					if deep {
						cfg.LogFn("[xray] deep wintun cleanup (retry)")
						desktoptun.DeepPrepareBeforeStart(tunAdapter)
						if err := desktoptun.EnsureAdapterAbsent(tunAdapter); err != nil {
							cfg.LogFn("[xray] adapter cleanup: %v", err)
						}
						if err := desktoptun.ForceReleaseWintunPool(tunAdapter); err != nil {
							cfg.LogFn("[xray] wintun pool: %v", err)
						}
					} else {
						desktoptun.PrepareBeforeStart(tunAdapter)
						_ = desktoptun.QuickReleaseWintunPool(tunAdapter)
					}
					time.Sleep(200 * time.Millisecond)
				}
				newXrayRunner := func() *wbxray.Runner {
					return wbxray.NewRunner(wbxray.Config{
						AdapterName:       tunAdapter,
						TunIP:             tunIP,
						TunGateway:        tunPeer,
						TunPrefix:         24,
						MTU:               tunMTU,
						SocksHost:         common.SocksLocalhostIP,
						SocksPort:         socksPort,
						Mode:              cfg.RoutingMode,
						CustomRulesJSON:   cfg.CustomRoutingJSON,
						SignalingHosts:    common.WBBypassHosts(""),
						EgressInterface:   egressAlias,
						EgressIfIndex:     egressIdx,
						EgressSendThrough: egressIP,
					}, cfg.LogFn)
				}
				waitPresent := func(xr *wbxray.Runner, label string, timeout time.Duration) error {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					deadline := time.Now().Add(timeout)
					for time.Now().Before(deadline) {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						if err, ok := xr.Exited(); ok {
							if err != nil {
								return fmt.Errorf("xray exited early: %w", err)
							}
							return fmt.Errorf("xray exited before adapter ready")
						}
						// Windows may assign a localized alias (not WDTT-WB) to the
						// freshly created wintun device — detect the Xray/Wintun
						// netdev and rename it to the wanted alias.
						if desktoptun.EnsureTunAdapterReady(tunAdapter) {
							cfg.LogFn("[xray] adapter %q ready", tunAdapter)
							return nil
						}
						time.Sleep(300 * time.Millisecond)
					}
					err := fmt.Errorf("adapter %q not present within %s", tunAdapter, timeout)
					if label != "" {
						cfg.LogFn("[xray] adapter %s: %v", label, err)
					} else {
						cfg.LogFn("[xray] adapter: %v", err)
					}
					if snap := desktoptun.WintunAdapterSnapshot(); snap != "" {
						cfg.LogFn("[xray] visible wintun adapters: %s", snap)
					}
					return err
				}
				setupTun := func() error {
					return routeShell.FinishTunSetup(tunIP, tunMask, tunPeer, tunMTU)
				}
				adapterReady := false
				var xr *wbxray.Runner
				for attempt := 0; attempt < 3; attempt++ {
					if attempt > 0 {
						if xrayRunner != nil {
							xrayRunner.Stop()
							_ = desktoptun.QuickReleaseWintunPool(tunAdapter)
						}
						time.Sleep(800 * time.Millisecond)
					}
					prepXray(attempt > 0)
					xr = newXrayRunner()
					xrayRunner = xr
					if err := xr.Launch(ctx, cfg.XrayBinary); err != nil {
						cfg.LogFn("[xray] launch: %v", err)
						continue
					}
					label := ""
					if attempt > 0 {
						label = fmt.Sprintf("retry %d", attempt)
					}
					if err := waitPresent(xr, label, 20*time.Second); err != nil {
						if ctx.Err() != nil {
							return
						}
						continue
					}
					adapterReady = true
					break
				}
				if !adapterReady {
					emitStatus("TUN_UNAVAILABLE")
					return
				}
				if err := setupTun(); err != nil {
					cfg.LogFn("[xray] tun setup: %v", err)
					if ctx.Err() != nil {
						return
					}
					emitStatus("TUN_UNAVAILABLE")
					return
				}
				bypass.installAtTunStart()
				tunUp.Store(true)
				emitStatus("TUN_ACTIVE")
				cfg.LogFn("TUN ACTIVE on %q — xray VPN mode=%s socks=127.0.0.1:%d", tunAdapter, cfg.RoutingMode, socksPort)
				runWarmup()
				return
			}

			err := tun.Start()
			if err != nil {
				cfg.LogFn("[tun] start: %v — повтор после очистки адаптера", err)
				desktoptun.PrepareBeforeStart(tunAdapter)
				time.Sleep(500 * time.Millisecond)
				err = tun.Start()
			}
			if err != nil {
				cfg.LogFn("[tun] start: %v", err)
				emitStatus("TUN_UNAVAILABLE")
				return
			}
			bypass.installAtTunStart()
			tunUp.Store(true)
			emitStatus("TUN_ACTIVE")
			cfg.LogFn("TUN ACTIVE on %q — netstack VPN via WB Stream", tunAdapter)
			runWarmup()
		})
	}

	onCandidate := func(target int, candidateOrSDP string) {
		if bypass != nil {
			bypass.noteRemoteCandidate(target, candidateOrSDP)
		}
	}

	resetJoiner := func(reason string) {
		mu.Lock()
		if joiner != nil {
			joiner.Close()
			joiner = nil
		}
		dialer.set(nil)
		mu.Unlock()
		sessionstats.Reset()
		if reason != "" {
			cfg.LogFn("[wbt] %s — joiner cleared", reason)
			emitStatus("TUNNEL_RECONNECTING")
		}
	}

	// iOS-style: keep joiner across WebRTC session recycle so onConnected can SwapTunnel.
	sessionEnded := func(reason string) {
		if ctx.Err() != nil {
			resetJoiner(reason)
			return
		}
		if reason != "" {
			cfg.LogFn("[wbt] %s — joiner kept for rebound", reason)
			emitStatus("TUNNEL_RECONNECTING")
		}
	}

	onConnected := func(t tunnel.DataTunnel) {
		mu.Lock()
		defer mu.Unlock()
		if joiner != nil {
			if err := joiner.SwapTunnel(t, nil); err != nil {
				cfg.LogFn("[wbt] tunnel rebound failed: %v — rebuilding joiner", err)
				joiner.Close()
				joiner = nil
			} else {
				cfg.LogFn("[wbt] tunnel rebound (WebRTC carrier swap)")
				signalCarrierRebound()
				if tun != nil || routeShell != nil {
					go bringUpVPN()
				} else if cfg.SocksOnly {
					// SOCKS already listening; do NOT re-emit SOCKS_READY —
					// that falsely marked the tunnel active before Listen.
				} else {
					runWarmup()
				}
				return
			}
		}
		j, err := wbtunnel.NewJoiner(ctx, t, cfg.SocksUser, cfg.SocksPass, cfg.LogFn, nil)
		if err != nil {
			cfg.LogFn("[wbt] joiner init: %v", err)
			return
		}
		joiner = j
		if !cfg.UseXray && !cfg.SocksOnly {
			dialer.set(j)
		}
		mode := "netstack"
		if cfg.UseXray {
			mode = "xray"
		}
		if cfg.SocksOnly {
			mode = "socks"
		}
		cfg.LogFn("TUNNEL CONNECTED mode=wbt (%s)", mode)
		emitStatus("TUNNEL_CONNECTED")
		if tun != nil || routeShell != nil {
			go bringUpVPN()
		} else if cfg.SocksOnly {
			go startSocksOnly(j)
		} else {
			runWarmup()
		}
	}

	joinLoopWG.Add(1)
	go func() {
		defer joinLoopWG.Done()
		runJoinLoop(ctx, roomID, cfg.DisplayName, fps, batch, cfg.DualTrack, bypass, settingEngine, cfg.LogFn, onConnected, onCandidate, func() {
			sessionEnded("WebRTC session ended")
		}, func(sess *wbstream.Session) {
			mu.Lock()
			activeSess = sess
			mu.Unlock()
		}, func(sess *wbstream.Session) {
			mu.Lock()
			if activeSess == sess {
				activeSess = nil
			}
			mu.Unlock()
		})
	}()

	if cfg.RecoverCh != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case req, ok := <-cfg.RecoverCh:
					if !ok {
						return
					}
					mu.Lock()
					j := joiner
					s := activeSess
					bp := bypass
					mu.Unlock()
					if bp != nil {
						bp.ensureHosts("", true)
					}
					if !req.ForceSession && j != nil {
						if rerr := j.RestartLink(true); rerr == nil {
							cfg.LogFn("[wbt] client recovery — KCP+smux без снятия VPN")
							runWarmup()
							continue
						} else {
							cfg.LogFn("[wbt] client recovery — KCP restart failed: %v", rerr)
						}
					}
					if s != nil {
						cfg.LogFn("[wbt] client recovery — WebRTC session без снятия VPN")
						s.Close()
					}
					// Do not resetJoiner — onConnected SwapTunnel rebinds carrier (iOS-style).
				}
			}
		}()
	}

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rx, tx, rtt := sessionstats.Snapshot()
				mu.Lock()
				if joiner != nil {
					if r := joiner.RTTMs(); r > 0 {
						rtt = r
					}
				}
				mu.Unlock()
				if cfg.OnStats != nil {
					cfg.OnStats(int64(rx), int64(tx), int64(rtt), int64(fps))
				}
			}
		}
	}()

	<-ctx.Done()

	cfg.LogFn("[wbjrunner] shutting down, снимаю TUN/маршруты")
	mu.Lock()
	if socksLn != nil {
		_ = socksLn.Close()
		socksLn = nil
	}
	mu.Unlock()
	// Drop smux/KCP first so gVisor TCP relays exit instead of blocking tun.Stop().
	mu.Lock()
	j := joiner
	joiner = nil
	dialer.set(nil)
	mu.Unlock()
	if j != nil {
		closeDone := make(chan struct{})
		go func() {
			j.Close()
			close(closeDone)
		}()
		select {
		case <-closeDone:
		case <-time.After(3 * time.Second):
			cfg.LogFn("[wbjrunner] joiner Close timeout")
		}
	}

	joinWait := make(chan struct{})
	go func() {
		joinLoopWG.Wait()
		close(joinWait)
	}()
	select {
	case <-joinWait:
	case <-time.After(2 * time.Second):
		cfg.LogFn("[wbjrunner] join loop stop timeout")
	}

	// Wait for an in-flight VPN bring-up to notice ctx and bail before
	// touching the adapter (it re-checks ctx on every launch step).
	select {
	case setupGate <- struct{}{}:
	case <-time.After(8 * time.Second):
		cfg.LogFn("[wbjrunner] VPN bring-up still busy — принудительный teardown")
	}

	if xrayRunner != nil {
		xrayRunner.Stop()
		time.Sleep(800 * time.Millisecond)
	}
	if routeShell != nil {
		routeShell.Stop()
	}
	if cfg.UseXray || routeShell != nil {
		desktoptun.TeardownTunAdapter(tunAdapter)
		cfg.LogFn("[desktoptun] tun adapter %q removed", tunAdapter)
	}
	if tun != nil {
		stopDone := make(chan struct{})
		go func() {
			tun.Stop()
			close(stopDone)
		}()
		select {
		case <-stopDone:
		case <-time.After(4 * time.Second):
			cfg.LogFn("[wbjrunner] tun stop timeout")
			desktoptun.EmergencyDown(tunAdapter)
		}
	}
	time.Sleep(100 * time.Millisecond)
	return ctx.Err()
}

type lazyJoinerDialer struct {
	mu sync.Mutex
	j  *wbtunnel.Joiner
}

func (d *lazyJoinerDialer) set(j *wbtunnel.Joiner) {
	d.mu.Lock()
	d.j = j
	d.mu.Unlock()
}

func (d *lazyJoinerDialer) current() *wbtunnel.Joiner {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.j
}

func (d *lazyJoinerDialer) DialTCP(ctx context.Context, host string, port int) (net.Conn, error) {
	j := d.current()
	if j == nil {
		return nil, fmt.Errorf("wbt: tunnel not ready")
	}
	return j.DialTCP(ctx, host, port)
}

func (d *lazyJoinerDialer) DialUDP(host string, port int) (net.PacketConn, error) {
	j := d.current()
	if j == nil {
		return nil, fmt.Errorf("wbt: tunnel not ready")
	}
	return j.DialUDP(host, port)
}

func runJoinLoop(
	ctx context.Context,
	roomID, name string,
	vp8FPS, vp8Batch int,
	dualTrack bool,
	bypass *tunBypass,
	settingEngine *webrtc.SettingEngine,
	logFn func(string, ...any),
	onConnected func(tunnel.DataTunnel),
	onCandidate func(int, string),
	onSessionEnd func(),
	setActiveSess func(*wbstream.Session),
	clearActiveSess func(*wbstream.Session),
) {
	const (
		baseRetry  = 4 * time.Second
		maxBackoff = 60 * time.Second
	)
	backoff := baseRetry
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		var delay time.Duration
		if err := runOnce(ctx, roomID, name, vp8FPS, vp8Batch, dualTrack, bypass, settingEngine, logFn, onConnected, onCandidate, onSessionEnd, setActiveSess, clearActiveSess); err != nil {
			if ctx.Err() != nil {
				return
			}
			errMsg := err.Error()
			if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "guest-register") {
				delay = backoff
				if backoff < maxBackoff {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
			} else {
				delay = baseRetry
				backoff = baseRetry
			}
			logFn("[wbt] session: %v, retry in %s", err, delay)
		} else {
			delay = baseRetry
			backoff = baseRetry
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func runOnce(
	ctx context.Context,
	roomID, name string,
	vp8FPS, vp8Batch int,
	dualTrack bool,
	bypass *tunBypass,
	settingEngine *webrtc.SettingEngine,
	logFn func(string, ...any),
	onConnected func(tunnel.DataTunnel),
	onCandidate func(int, string),
	onSessionEnd func(),
	setActiveSess func(*wbstream.Session),
	clearActiveSess func(*wbstream.Session),
) error {
	tSession := time.Now()
	logFn("[wbt] session start dualTrack=%v fps=%d batch=%d", dualTrack, vp8FPS, vp8Batch)
	obf, err := tunnel.NewTunnelObfuscator(tunnel.DeriveSecretFromJoinLink(roomID))
	if err != nil {
		return fmt.Errorf("obfuscator: %w", err)
	}
	logFn("[wbt] obf localEpoch=0x%08x", obf.LocalEpoch())

	var authClient *http.Client
	var netDial func(context.Context, string, string) (net.Conn, error)
	if bypass != nil {
		authClient = bypass.signalingHTTPClient()
		netDial = bypass.dialBypassHost
	}

	tAuth := time.Now()
	id, roomToken, _, serverURL, authErr := wbstream.AuthAndGetToken(authClient, roomID, name)
	if authErr != nil {
		return fmt.Errorf("auth: %w", authErr)
	}
	logFn("[wbt] room=%s server=%s (auth %v)", id, serverURL, time.Since(tAuth).Round(time.Millisecond))
	if bypass != nil && bypass.ensureHosts(serverURL, false) {
		logFn("[wbt] signaling bypass ready")
	}

	sessCfg := wbstream.SessionConfig{
		RoomToken:     roomToken,
		ServerURL:     serverURL,
		DisplayName:   name,
		TunnelMode:    wbstream.TunnelModeVideo,
		Obfuscator:    obf,
		LogFn:         logFn,
		SettingEngine: settingEngine,
		VP8FPS:        vp8FPS,
		VP8Batch:      vp8Batch,
		ScreenShare:   dualTrack,
		IsJoiner:      true,
		UseWBT:        true,
	}
	if bypass != nil {
		sessCfg.ResolveICEHost = bypass.resolveICEHost
		sessCfg.OnJoin = bypass.onJoin
		sessCfg.NetDialContext = netDial
	}
	sess := wbstream.NewSession(sessCfg)
	sess.OnConnected = onConnected
	sess.OnRemoteCandidate = onCandidate
	sess.OnTunnelLost = func() {
		if ctx.Err() != nil {
			return
		}
		if onSessionEnd != nil {
			onSessionEnd()
		}
	}
	if setActiveSess != nil {
		setActiveSess(sess)
	}
	defer func() {
		if clearActiveSess != nil {
			clearActiveSess(sess)
		}
	}()

	tStart := time.Now()
	if err := sess.Start(); err != nil {
		sess.Close()
		return fmt.Errorf("start: %w", err)
	}
	logFn("[wbt] signaling Start ok in %v (session elapsed %v)", time.Since(tStart).Round(time.Millisecond), time.Since(tSession).Round(time.Millisecond))
	go func() {
		<-ctx.Done()
		sess.Close()
	}()
	select {
	case <-sess.Done():
	case <-ctx.Done():
	}
	sess.Close()
	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		logFn("[wbt] session close timeout")
	}
	logFn("[wbt] session ended after %v", time.Since(tSession).Round(time.Millisecond))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if onSessionEnd != nil {
		onSessionEnd()
	}
	return nil
}
