package backend

import (
	"bufio"
	"strings"
	"sync"
	"sync/atomic"
)

var softReconnectPreserve atomic.Bool

// SetSoftReconnectPreserve — soft-reconnect: не трогать wg-turn, только перезапуск core/воркеров.
func SetSoftReconnectPreserve(v bool) { softReconnectPreserve.Store(v) }

// SoftReconnectPreserve reports whether the next applyWGConfig should skip teardown.
func SoftReconnectPreserve() bool { return softReconnectPreserve.Load() }

const wgIface = "wg-turn"

// vkTransportCIDRs — подсети VK TURN/WebRTC, на которых держится САМ транспорт туннеля
// (воркеры релеят WG-трафик через эти сервера). ВСЕГДА напрямую — иначе петля
// маршрутизации (туннель пошёл бы сам в себя) и соединение не поднимется.
var vkTransportCIDRs = []string{
	"90.156.0.0/16",    // VK TURN
	"95.163.0.0/16",    // VK TURN
	"155.212.192.0/20", // OK/VK (calls.okcdn.ru)
}

// vkWebCIDRs — веб/API/CDN ВКонтакте (queuev4, login, id, st*-cdn, userapi).
// По умолчанию напрямую (быстрый vk.com в браузере). «VK через туннель» убирает их
// из прямых маршрутов → трафик VK-сайта/приложения идёт в wg-turn (через VPN).
var vkWebCIDRs = []string{
	"87.240.128.0/18", // VK
	"87.240.192.0/19", // VK
	"93.186.224.0/19", // VK API (queuev4/login/id .237.x), userapi
	"95.142.192.0/19", // VK static CDN (st4-9 .203.x)
	"95.213.0.0/18",   // VK (login/id)
	"185.16.28.0/22",  // VK
	"194.67.64.0/18",  // VK
	"195.82.146.0/23", // VK
}

// vkExcludeCIDRs — всё, что по умолчанию идёт напрямую при подключении (транспорт + веб).
var vkExcludeCIDRs = append(append([]string{}, vkTransportCIDRs...), vkWebCIDRs...)

var (
	vkRouteMu          sync.Mutex
	wgGateway          string
	vkExcludeInstalled bool        // транспорт+веб установлены напрямую (на коннекте)
	vkWebDirect        bool        // веб-подсети сейчас напрямую (true=VK direct, false=через туннель)
	vkThroughTunnel    atomic.Bool // true = VK веб/API через туннель (всегда вкл нативно)
)

// VK-веб через туннель включён нативно и не настраивается из UI.
func init() { vkThroughTunnel.Store(true) }

// rememberWGGateway сохраняет шлюз для переключения маршрутов VK.
func rememberWGGateway(gw string) {
	vkRouteMu.Lock()
	wgGateway = gw
	vkRouteMu.Unlock()
}

func clearWGRouteState() {
	resetVKExcludeRouteTracking()
	vkRouteMu.Lock()
	wgGateway = ""
	vkExcludeInstalled = false
	vkWebDirect = false
	vkRouteMu.Unlock()
}

// markVKExcludeInstalled — вызывается после установки прямых VK-маршрутов на коннекте.
// По умолчанию веб-подсети напрямую (VK direct).
func markVKExcludeInstalled() {
	vkRouteMu.Lock()
	vkExcludeInstalled = true
	vkWebDirect = true
	vkRouteMu.Unlock()
}

// SetVKThroughTunnel — задаёт режим и применяет немедленно, если туннель активен.
// true: веб-трафик VK идёт через VPN-туннель; false: VK напрямую (по умолчанию).
// Транспортные TURN-подсети (vkTransportCIDRs) всегда остаются напрямую.
func SetVKThroughTunnel(through bool) error {
	vkThroughTunnel.Store(through)
	return applyVKRouting()
}

// VKThroughTunnel сообщает текущий желаемый режим.
func VKThroughTunnel() bool { return vkThroughTunnel.Load() }

// applyVKRouting приводит прямые маршруты веб-подсетей VK в соответствие с vkThroughTunnel.
// Вызывается из SetVKThroughTunnel и после applyWGConfig (когда маршруты уже стоят).
func applyVKRouting() error {
	through := vkThroughTunnel.Load()
	vkRouteMu.Lock()
	gw := wgGateway
	installed := vkExcludeInstalled
	webDirect := vkWebDirect
	vkRouteMu.Unlock()
	if gw == "" || !installed {
		return nil // нет активного туннеля — применится при следующем applyWGConfig
	}
	// through=true ⇔ webDirect=false. Уже в нужном состоянии — выходим.
	if through == !webDirect {
		return nil
	}
	if through {
		if err := delVKRoutes(vkWebCIDRs); err != nil {
			return err
		}
		vkRouteMu.Lock()
		vkWebDirect = false
		vkRouteMu.Unlock()
		return nil
	}
	if err := addVKRoutes(gw, vkWebCIDRs); err != nil {
		return err
	}
	vkRouteMu.Lock()
	vkWebDirect = true
	vkRouteMu.Unlock()
	return nil
}

// wg-quick-only fields that wg setconf doesn't understand
var wgQuickOnlyFields = map[string]bool{
	"address": true, "dns": true, "mtu": true,
	"preup": true, "postup": true, "predown": true, "postdown": true,
	"saveconfig": true,
}

// parseWGConfig extracts Address, MTU, AllowedIPs and returns a wg-setconf-compatible config.
func parseWGConfig(conf string) (addr, mtu string, allowedIPs []string, wgConf string) {
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(conf))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 {
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			val := strings.TrimSpace(parts[1])
			switch key {
			case "address":
				addr = val
				continue
			case "mtu":
				mtu = val
				continue
			case "allowedips":
				for _, cidr := range strings.Split(val, ",") {
					if c := strings.TrimSpace(cidr); c != "" {
						allowedIPs = append(allowedIPs, c)
					}
				}
			default:
				if wgQuickOnlyFields[key] {
					continue
				}
			}
		}
		out.WriteString(line + "\n")
	}
	wgConf = out.String()
	return
}
