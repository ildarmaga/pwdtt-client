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
	primaryIP  atomic.Value // net.IP (IPv4) — адрес TUN с первого RAWCONF
}

func newWGConfigGate(ch chan<- string, tunnelMode string, mtu int, primaryHint string) *wgConfigGate {
	if tunnelMode != "raw" {
		tunnelMode = "wg"
	}
	// WG без канала = нет GETCONF. RAW: ch может быть nil у вторичных групп,
	// но gate всё равно нужен — каждый воркер шлёт свой RAWCONF.
	if ch == nil && tunnelMode != "raw" {
		return nil
	}
	g := &wgConfigGate{ch: ch, tunnelMode: tunnelMode, mtu: mtu}
	if tunnelMode == "raw" {
		if ip := net.ParseIP(strings.TrimSpace(primaryHint)); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				g.primaryIP.Store(append(net.IP(nil), ip4...))
			}
		}
	}
	return g
}

func (g *wgConfigGate) delivered() bool {
	return g == nil || g.sent.Load() == 1
}

func (g *wgConfigGate) PrimaryIP() net.IP {
	if g == nil {
		return nil
	}
	v, _ := g.primaryIP.Load().(net.IP)
	return v
}

// needsConfig — для WG один GETCONF на группу; для RAW каждый DTLS-коннект
// заново шлёт RAWCONF (сервер выдаёт уникальный IP на сессию).
func (g *wgConfigGate) needsConfig() bool {
	if g == nil {
		return false
	}
	if g.tunnelMode == "raw" {
		return true
	}
	return !g.delivered()
}

// tryDeliver возвращает (ok, workerIP, err).
// RAW: каждый воркер получает свой IP; первый конфиг поднимает TUN (primaryIP).
// WG: как раньше — один GETCONF на группу.
func (g *wgConfigGate) tryDeliver(sessionID int, conn net.Conn, localPort, deviceID, password string) (bool, net.IP, error) {
	if g == nil {
		return false, nil, nil
	}
	if g.tunnelMode != "raw" && g.delivered() {
		return false, nil, nil
	}

	if g.tunnelMode == "raw" {
		conf, err := RequestRawConfig(conn, deviceID, password, g.mtu)
		if err != nil {
			if strings.Contains(err.Error(), "FATAL_AUTH") {
				return false, nil, err
			}
			log.Printf("[ВОРКЕР #%d] Ошибка RAWCONF: %v", sessionID, err)
			return false, nil, nil
		}
		if conf == "" {
			log.Printf("[ВОРКЕР #%d] Сервер ещё не выдал RAW-конфиг, повторим позже", sessionID)
			return false, nil, nil
		}
		workerIP := parseRawConfIP(conf)
		if workerIP == nil {
			log.Printf("[ВОРКЕР #%d] RAWCONF без IP: %q", sessionID, trimProtoPreview(conf, 64))
			return false, nil, nil
		}
		if g.sent.CompareAndSwap(0, 1) {
			// Soft-reconnect: primary уже с TUN — не перезаписывать.
			if g.PrimaryIP() == nil {
				g.primaryIP.Store(append(net.IP(nil), workerIP...))
			}
			if g.ch != nil {
				select {
				case g.ch <- conf:
					log.Printf("[ВОРКЕР #%d] RAW-конфиг получен (primary ip=%s, worker=%s)", sessionID, g.PrimaryIP(), workerIP)
				default:
					log.Printf("[ВОРКЕР #%d] RAW-конфиг уже доставлен", sessionID)
				}
			} else {
				log.Printf("[ВОРКЕР #%d] RAW-сессия ip=%s (primary=%s)", sessionID, workerIP, g.PrimaryIP())
			}
		} else {
			log.Printf("[ВОРКЕР #%d] RAW-сессия ip=%s (primary=%s)", sessionID, workerIP, g.PrimaryIP())
		}
		return true, append(net.IP(nil), workerIP...), nil
	}

	// WG: один GETCONF
	if !g.inFlight.CompareAndSwap(0, 1) {
		return false, nil, nil
	}
	defer g.inFlight.Store(0)

	conf, err := RequestConfig(conn, localPort, deviceID, password)
	if err != nil {
		if strings.Contains(err.Error(), "FATAL_AUTH") {
			return false, nil, err
		}
		log.Printf("[ВОРКЕР #%d] Ошибка конфига: %v", sessionID, err)
		return false, nil, nil
	}
	if conf == "" {
		log.Printf("[ВОРКЕР #%d] Сервер ещё не выдал конфиг, повторим позже", sessionID)
		return false, nil, nil
	}

	if g.sent.CompareAndSwap(0, 1) {
		select {
		case g.ch <- conf:
			log.Printf("[ВОРКЕР #%d] Конфиг получен", sessionID)
		default:
			log.Printf("[ВОРКЕР #%d] Конфиг уже был доставлен другим воркером", sessionID)
		}
	}
	return true, nil, nil
}
