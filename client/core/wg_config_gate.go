package core

import (
	"log"
	"net"
	"strings"
	"sync/atomic"
)

// wgConfigGate — один конфиг на группу (WG GETCONF или RAW RAWCONF).
type wgConfigGate struct {
	ch         chan<- string
	tunnelMode string // wg|raw
	mtu        int
	sent       atomic.Int32
	inFlight   atomic.Int32
}

func newWGConfigGate(ch chan<- string, tunnelMode string, mtu int) *wgConfigGate {
	if ch == nil {
		return nil
	}
	if tunnelMode != "raw" {
		tunnelMode = "wg"
	}
	return &wgConfigGate{ch: ch, tunnelMode: tunnelMode, mtu: mtu}
}

func (g *wgConfigGate) delivered() bool {
	return g == nil || g.sent.Load() == 1
}

// needsConfig — для WG один GETCONF на группу; для RAW каждый DTLS-коннект
// заново шлёт RAWCONF (сервер привязывает raw-сессию к этому conn).
func (g *wgConfigGate) needsConfig() bool {
	if g == nil {
		return false
	}
	if g.tunnelMode == "raw" {
		return true
	}
	return !g.delivered()
}

func (g *wgConfigGate) tryDeliver(sessionID int, conn net.Conn, localPort, deviceID, password string) (bool, error) {
	if g == nil {
		return false, nil
	}
	if g.tunnelMode != "raw" && g.delivered() {
		return false, nil
	}
	if !g.inFlight.CompareAndSwap(0, 1) {
		// RAW: другой воркер уже в полёте — этот conn не для datapath.
		if g.tunnelMode == "raw" {
			return false, nil
		}
		return false, nil
	}
	defer func() {
		g.inFlight.Store(0)
	}()

	var conf string
	var err error
	if g.tunnelMode == "raw" {
		conf, err = RequestRawConfig(conn, deviceID, password, g.mtu)
	} else {
		conf, err = RequestConfig(conn, localPort, deviceID, password)
	}
	if err != nil {
		if strings.Contains(err.Error(), "FATAL_AUTH") {
			return false, err
		}
		log.Printf("[ВОРКЕР #%d] Ошибка конфига: %v", sessionID, err)
		return false, nil
	}
	if conf == "" {
		log.Printf("[ВОРКЕР #%d] Сервер ещё не выдал конфиг, повторим позже", sessionID)
		return false, nil
	}

	if g.sent.CompareAndSwap(0, 1) {
		select {
		case g.ch <- conf:
			if g.tunnelMode == "raw" {
				log.Printf("[ВОРКЕР #%d] RAW-конфиг получен", sessionID)
			} else {
				log.Printf("[ВОРКЕР #%d] Конфиг получен", sessionID)
			}
		default:
			log.Printf("[ВОРКЕР #%d] Конфиг уже был доставлен другим воркером", sessionID)
		}
	} else if g.tunnelMode == "raw" {
		log.Printf("[ВОРКЕР #%d] RAW-сессия перепривязана к новому DTLS", sessionID)
	}
	return true, nil
}
