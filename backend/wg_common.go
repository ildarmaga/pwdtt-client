package backend

import (
	"bufio"
	"strings"
	"sync"
)

const wgIface = "wg-turn"

// vkExcludeCIDRs — VK TURN/DTLS: по умолчанию напрямую (минимальная задержка к relay).
// SetVKThroughTunnel(true) — гнать эти подсети через VPN-туннель.
var vkExcludeCIDRs = []string{
	"87.240.128.0/18",  // VK
	"87.240.192.0/19",  // VK
	"90.156.0.0/16",    // VK TURN
	"93.186.224.0/21",  // VK
	"95.142.192.0/21",  // VK
	"95.163.0.0/16",    // VK TURN
	"95.213.0.0/18",    // VK (login/id)
	"155.212.192.0/20", // OK/VK (calls.okcdn.ru)
	"185.16.28.0/22",   // VK
	"194.67.64.0/18",   // VK
	"195.82.146.0/23",  // VK
}

var (
	vkRouteMu         sync.Mutex
	wgGateway         string
	vkExcludeInstalled bool
)

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
	vkRouteMu.Unlock()
}

// SetVKThroughTunnel — true: VK через VPN-туннель; false: VK напрямую (по умолчанию).
func SetVKThroughTunnel(through bool) error {
	vkRouteMu.Lock()
	gw := wgGateway
	installed := vkExcludeInstalled
	vkRouteMu.Unlock()
	if gw == "" {
		return nil
	}
	if through {
		if !installed {
			return nil
		}
		if err := uninstallVKExcludeRoutes(); err != nil {
			return err
		}
		vkRouteMu.Lock()
		vkExcludeInstalled = false
		vkRouteMu.Unlock()
		return nil
	}
	if installed {
		return nil
	}
	if err := installVKExcludeRoutes(gw); err != nil {
		return err
	}
	vkRouteMu.Lock()
	vkExcludeInstalled = true
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
