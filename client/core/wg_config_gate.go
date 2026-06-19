package core

import (
	"log"
	"net"
	"strings"
	"sync/atomic"
)

// wgConfigGate — один WireGuard-конфиг на группу: любой воркер после DTLS
// может запросить GETCONF, если предыдущий завис/упал по таймауту.
type wgConfigGate struct {
	ch       chan<- string
	sent     atomic.Int32
	inFlight atomic.Int32
}

func newWGConfigGate(ch chan<- string) *wgConfigGate {
	if ch == nil {
		return nil
	}
	return &wgConfigGate{ch: ch}
}

func (g *wgConfigGate) delivered() bool {
	return g == nil || g.sent.Load() == 1
}

func (g *wgConfigGate) tryDeliver(sessionID int, conn net.Conn, localPort, deviceID, password string) (bool, error) {
	if g == nil || g.delivered() {
		return false, nil
	}
	if !g.inFlight.CompareAndSwap(0, 1) {
		return false, nil
	}
	defer func() {
		if g.sent.Load() == 0 {
			g.inFlight.Store(0)
		}
	}()

	conf, err := RequestConfig(conn, localPort, deviceID, password)
	if err != nil {
		if strings.Contains(err.Error(), "FATAL_AUTH") {
			return false, err
		}
		log.Printf("[ВОРКЕР #%d] Ошибка конфига: %v", sessionID, err)
		return false, nil
	}
	if conf == "" {
		log.Printf("[ВОРКЕР #%d] Сервер ещё не выдал WireGuard-конфиг, повторим позже", sessionID)
		return false, nil
	}

	select {
	case g.ch <- conf:
		g.sent.Store(1)
		log.Printf("[ВОРКЕР #%d] Конфиг получен", sessionID)
		return true, nil
	default:
		g.sent.Store(1)
		log.Printf("[ВОРКЕР #%d] Конфиг уже был доставлен другим воркером", sessionID)
		return true, nil
	}
}
