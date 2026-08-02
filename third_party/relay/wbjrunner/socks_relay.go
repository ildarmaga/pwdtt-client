package wbjrunner

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
	"github.com/ildarmaga/whitelist-bypass/relay/tunnel"
	"github.com/ildarmaga/whitelist-bypass/relay/wbstream"
)

// runSocksRelayBridge is the kulikov0-style WB path: VP8 + RelayBridge + local SOCKS5.
// No KCP/smux, no wintun/xray — point v2rayN at 127.0.0.1:SocksPort.
func runSocksRelayBridge(ctx context.Context, cfg Config) error {
	sessionstats.Reset()

	host := cfg.SocksHost
	if host == "" {
		host = common.SocksLocalhostIP
	}
	port := cfg.SocksPort
	if port <= 0 {
		port = 10809
	}
	fps, batch := cfg.VP8FPS, cfg.VP8Batch
	if fps <= 0 {
		fps = 30
	}
	if batch <= 0 {
		batch = 64
	}
	dual := cfg.DualTrack
	cfg.LogFn("[relay] SOCKS-only (kulikov0 RelayBridge) fps=%d batch=%d dualTrack=%v port=%d", fps, batch, dual, port)

	room := strings.TrimSpace(cfg.Room)
	if room == "" {
		return fmt.Errorf("room is required")
	}
	roomID := wbstream.ParseRoomID(room)

	emitStatus := func(code string) {
		if ctx.Err() != nil {
			return
		}
		if cfg.OnStatus != nil {
			cfg.OnStatus(code)
		}
	}

	var (
		mu        sync.Mutex
		bridge    *tunnel.RelayBridge
		socksOnce sync.Once
		activeSess *wbstream.Session
	)

	onConnected := func(t tunnel.DataTunnel) {
		readBuf := common.VP8BufSize
		if _, ok := t.(*tunnel.DCTunnel); ok {
			readBuf = common.DCBufSize
		}
		mu.Lock()
		defer mu.Unlock()
		if bridge != nil {
			// Keep live SOCKS TCP/UDP across ICE rebind (sub offer / sub ICE).
			// SwapTunnel(reset=true) was closing v2rayN flows mid-handshake →
			// "unknown conn" NACKs and watchdog false-kills. Creator/iOS already
			// use KeepConns(false).
			bridge.SwapTunnelKeepConns(t, false)
			cfg.LogFn("[relay] tunnel swapped after reconnect (keep conns)")
			return
		}
		bridge = tunnel.NewRelayBridgeWithAuth(t, "joiner", readBuf, cfg.LogFn, cfg.SocksUser, cfg.SocksPass)
		bridge.SetPersistentListener(true)
		if activeSess != nil {
			bridge.SetOnConfigAck(activeSess.MarkConfigAcked)
		}
		bridge.MarkReady()
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		socksOnce.Do(func() {
			go func() {
				if err := bridge.ListenSOCKS(addr); err != nil {
					cfg.LogFn("[socks] listen: %v", err)
					emitStatus("SOCKS_UNAVAILABLE")
				}
			}()
			cfg.LogFn("wbt: SOCKS5 on %s (v2rayN/V2BOX → этот адрес)", addr)
			if cfg.OnSocksReady != nil {
				cfg.OnSocksReady(host, port, cfg.SocksUser, cfg.SocksPass)
			}
			emitStatus("SOCKS_READY")
		})
		cfg.LogFn("TUNNEL CONNECTED mode=relay (socks)")
		emitStatus("TUNNEL_CONNECTED")
	}

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if cfg.OnStats == nil {
					continue
				}
				rx, tx, rtt := sessionstats.Snapshot()
				cfg.OnStats(int64(rx), int64(tx), int64(rtt), 0)
			}
		}
	}()

	var joinLoopWG sync.WaitGroup
	joinLoopWG.Add(1)
	go func() {
		defer joinLoopWG.Done()
		runJoinLoopRelay(ctx, roomID, cfg.DisplayName, fps, batch, dual, cfg.LogFn, onConnected, func(sess *wbstream.Session) {
			mu.Lock()
			activeSess = sess
			if bridge != nil {
				bridge.SetOnConfigAck(sess.MarkConfigAcked)
			}
			mu.Unlock()
		})
	}()

	<-ctx.Done()
	cfg.LogFn("[wbjrunner] shutting down")
	mu.Lock()
	if bridge != nil {
		bridge.Close()
		bridge = nil
	}
	mu.Unlock()
	joinLoopWG.Wait()
	return ctx.Err()
}

func runJoinLoopRelay(
	ctx context.Context,
	roomID, name string,
	vp8FPS, vp8Batch int,
	dualTrack bool,
	logFn func(string, ...any),
	onConnected func(tunnel.DataTunnel),
	onSession func(*wbstream.Session),
) {
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := runOnceRelay(ctx, roomID, name, vp8FPS, vp8Batch, dualTrack, logFn, onConnected, onSession)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logFn("[relay] session: %v — retry in %s", err, delay)
		} else {
			logFn("[relay] session ended — rejoin in %s", delay)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 16*time.Second {
			delay *= 2
		}
	}
}

func runOnceRelay(
	ctx context.Context,
	roomID, name string,
	vp8FPS, vp8Batch int,
	dualTrack bool,
	logFn func(string, ...any),
	onConnected func(tunnel.DataTunnel),
	onSession func(*wbstream.Session),
) error {
	obf, err := tunnel.NewTunnelObfuscator(tunnel.DeriveSecretFromJoinLink(roomID))
	if err != nil {
		return fmt.Errorf("obfuscator: %w", err)
	}
	logFn("[relay] obf localEpoch=0x%08x", obf.LocalEpoch())

	id, roomToken, _, serverURL, authErr := wbstream.AuthAndGetToken(nil, roomID, name)
	if authErr != nil {
		return fmt.Errorf("auth: %w", authErr)
	}
	logFn("[relay] room=%s server=%s", id, serverURL)

	sess := wbstream.NewSession(wbstream.SessionConfig{
		RoomToken:   roomToken,
		ServerURL:   serverURL,
		DisplayName: name,
		TunnelMode:  wbstream.TunnelModeVideo,
		Obfuscator:  obf,
		LogFn:       logFn,
		VP8FPS:      vp8FPS,
		VP8Batch:    vp8Batch,
		ScreenShare: dualTrack,
		IsJoiner:    true,
		// UseWBT=false → RelayBridge framing + config ping-pong (kulikov0).
	})
	sess.OnConnected = onConnected
	if onSession != nil {
		onSession(sess)
	}
	sess.OnTunnelLost = func() {
		logFn("[relay] tunnel lost")
	}

	if err := sess.Start(); err != nil {
		sess.Close()
		return fmt.Errorf("start: %w", err)
	}
	select {
	case <-ctx.Done():
		sess.Close()
		return ctx.Err()
	case <-sess.Done():
		sess.Close()
		return nil
	}
}
