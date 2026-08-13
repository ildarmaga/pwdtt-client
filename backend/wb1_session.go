package backend

import (
	"context"
	"fmt"
	"net"
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

	if displayName == "" {
		displayName = "WDTT"
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
		return fmt.Errorf("wb auth: %w", err)
	}

	key, err := wb1.DeriveKey(password, roomID)
	if err != nil {
		return err
	}

	m.emitLog("INFO", "[WB] LiveKit "+serverURL)
	sess, err := wb1.ConnectRoom(ctx, serverURL, roomToken)
	if err != nil {
		return fmt.Errorf("livekit: %w", err)
	}
	defer sess.Close()

	m.emitLog("INFO", "[WB] Жду creator в комнате (как в групповом звонке)…")
	waitCtx, waitCancel := context.WithTimeout(ctx, 45*time.Second)
	creator, err := sess.WaitCreator(waitCtx)
	waitCancel()
	if err != nil {
		return fmt.Errorf("creator не в комнате: %w", err)
	}
	m.emitLog("INFO", "[WB] creator "+creator.Name+" / "+creator.Identity)

	mux := wb1.NewMux(key, creator)
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
	go func() { _ = mux.Run(runCtx) }()

	addr := net.JoinHostPort(socksHost, fmt.Sprintf("%d", socksPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.onStatus("SOCKS_UNAVAILABLE")
		return fmt.Errorf("socks listen %s: %w", addr, err)
	}
	defer ln.Close()
	go func() {
		_ = wb1.ServeSOCKS(runCtx, ln, socksUser, socksPass, mux.Dial)
	}()

	m.mu.Lock()
	m.socksReady = true
	m.lastHealthy = time.Now()
	m.mu.Unlock()
	m.onStatus("SOCKS_READY")
	runtime.EventsEmit(m.ctx, "wb_socks_ready", socksHost, socksPort, socksUser, socksPass)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sess.Done():
			return fmt.Errorf("livekit disconnected")
		case <-ticker.C:
			pingCtx, pingCancel := context.WithTimeout(runCtx, 5*time.Second)
			rtt, err := mux.Ping(pingCtx)
			pingCancel()
			rttMs := int64(0)
			if err == nil {
				rttMs = rtt.Milliseconds()
				if rttMs <= 0 {
					rttMs = 1
				}
			}
			m.onStats(rx.Load(), tx.Load(), rttMs, 1)
		}
	}
}
