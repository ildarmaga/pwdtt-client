package backend

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ildarmaga/wdtt/pkg/wb1"
	"github.com/ildarmaga/wdtt/pkg/wbstream"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (m *WBManager) runWB1(ctx context.Context) error {
	m.mu.Lock()
	roomLink := m.room
	password := m.password
	displayName := m.displayName
	socksHost := m.socksHost
	socksPort := m.socksPort
	socksUser := m.socksUser
	socksPass := m.socksPass
	m.mu.Unlock()

	if displayName == "" || strings.EqualFold(displayName, "WDTT") {
		displayName = "PWDTT"
	}
	roomID := wbstream.ParseRoomID(roomLink)
	if roomID == "" {
		return fmt.Errorf("пустой room id")
	}
	if password == "" {
		return fmt.Errorf("нет пароля подписки — нужен для WDTT-WB1")
	}

	m.emitLog("INFO", "[WB] Гостевой вход в комнату WDTT-WB1…")
	_, roomToken, _, serverURL, err := wbstream.AuthAsGuest(nil, displayName, roomID)
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			m.emitLog("WARN", "[WB] guest-register 429 — пауза 20 с")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Second):
			}
		}
		return fmt.Errorf("wb auth: %w", err)
	}

	key, err := wb1.DeriveKey(password, roomID)
	if err != nil {
		return err
	}

	m.emitLog("INFO", "[WB] LiveKit "+serverURL+" (WB1 v3 reliable VP8)")
	sess, err := wb1.ConnectRoom(ctx, serverURL, roomToken)
	if err != nil {
		return fmt.Errorf("livekit: %w", err)
	}
	defer sess.Close()
	sess.SetCryptoKey(key)

	m.emitLog("INFO", "[WB] Жду creator в комнате…")
	waitCtx, waitCancel := context.WithTimeout(ctx, 12*time.Second)
	creator, err := sess.WaitCreator(waitCtx)
	waitCancel()
	if err != nil {
		return fmt.Errorf("creator не в комнате (%s): %w", strings.Join(sess.PeerLabels(), ","), err)
	}
	m.emitLog("INFO", "[WB] creator "+creator.Name+" / "+creator.Identity)

	mux := wb1.NewMux(key, sess.JoinerCarrier())
	mux.SetRoute(sess.LocalSID(), sess.CreatorSID())
	var rx, tx atomic.Int64
	mux.SetTrafficHook(func(up, down int64) {
		tx.Add(up)
		rx.Add(down)
		m.mu.Lock()
		now := time.Now()
		m.lastTrafficAt = now
		m.lastTrafficBytes += up + down
		if down > 0 {
			m.lastRxAt = now
			m.lastRxBytes += down
		}
		if up+down >= 8192 {
			m.lastFastTrafficAt = now
		}
		m.mu.Unlock()
	})
	mux.SetPeerHook(func() {
		m.mu.Lock()
		m.lastHealthy = time.Now()
		m.mu.Unlock()
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer mux.Close()
	muxDone := make(chan error, 1)
	go func() { muxDone <- mux.Run(runCtx) }()

	pingCtx, pingCancel := context.WithTimeout(runCtx, 5*time.Second)
	if _, err := mux.Ping(pingCtx); err != nil {
		pingCancel()
		return fmt.Errorf("creator не отвечает на ping: %w", err)
	}
	pingCancel()

	addr := net.JoinHostPort(socksHost, fmt.Sprintf("%d", socksPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.onStatus("SOCKS_UNAVAILABLE")
		return fmt.Errorf("socks listen %s: %w", addr, err)
	}
	defer ln.Close()
	dialTCP := func(ctx context.Context, dest string) (net.Conn, error) {
		m.emitLog("INFO", fmt.Sprintf("[WB] SOCKS CONNECT %s", dest))
		conn, err := mux.Dial(ctx, dest)
		if err != nil {
			return nil, err
		}
		return wrapTrafficConn(conn, dest), nil
	}
	go func() {
		_ = wb1.ServeSOCKSUDP(runCtx, ln, socksUser, socksPass, dialTCP, func(ctx context.Context, dest string) (net.Conn, error) {
			m.emitLog("INFO", fmt.Sprintf("[WB] SOCKS UDP %s", dest))
			conn, err := mux.Dial(ctx, wb1.UDPDest(dest))
			if err != nil {
				return nil, err
			}
			return wrapTrafficConn(conn, dest), nil
		})
	}()

	m.mu.Lock()
	m.socksReady = true
	m.lastHealthy = time.Now()
	m.mu.Unlock()
	m.onStatus("SOCKS_READY")
	runtime.EventsEmit(m.ctx, "wb_socks_ready", socksHost, socksPort, socksUser, socksPass)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	pingFailures := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sess.Done():
			return fmt.Errorf("livekit disconnected")
		case err := <-muxDone:
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("mux: %w", err)
			}
			return fmt.Errorf("mux closed")
		case <-ticker.C:
			if mux.Closed() {
				return fmt.Errorf("mux closed")
			}
			pingCtx, pingCancel := context.WithTimeout(runCtx, 5*time.Second)
			rtt, err := mux.Ping(pingCtx)
			pingCancel()
			rttMs := int64(0)
			if err == nil {
				pingFailures = 0
				rttMs = rtt.Milliseconds()
				if rttMs <= 0 {
					rttMs = 1
				}
			} else {
				pingFailures++
				if pingFailures >= 3 {
					ws := sess.VP8WriteStats()
					return fmt.Errorf("WB transport stalled: ping failures=%d write_last=%s write_max=%s rate=%.1fMbps: %w", pingFailures, ws.Last, ws.Max, float64(ws.RateBits)/1e6, err)
				}
			}
			m.onStats(rx.Load(), tx.Load(), rttMs, sess.VP8FPS())
		}
	}
}
