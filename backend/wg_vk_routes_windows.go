//go:build windows

package backend

import "sync"

var (
	activeVKExcludeRoutes []string
	vkExcludeRoutesMu     sync.Mutex
)

func installVKExcludeRoutes(gw string) error {
	vkExcludeRoutesMu.Lock()
	defer vkExcludeRoutesMu.Unlock()
	var added []string
	for _, cidr := range vkExcludeCIDRs {
		ip, mask, err := parseCIDR(cidr)
		if err != nil {
			continue
		}
		if run("route", "add", ip, "mask", mask, gw) == nil {
			added = append(added, cidr)
		}
	}
	activeVKExcludeRoutes = append(activeVKExcludeRoutes, added...)
	activeExcludeRoutes = append(activeExcludeRoutes, added...)
	return nil
}

func uninstallVKExcludeRoutes() error {
	vkExcludeRoutesMu.Lock()
	routes := append([]string(nil), activeVKExcludeRoutes...)
	activeVKExcludeRoutes = nil
	vkExcludeRoutesMu.Unlock()
	for _, cidr := range routes {
		ip, _, _ := parseCIDR(cidr)
		if ip != "" {
			_ = run("route", "delete", ip)
		}
	}
	filtered := activeExcludeRoutes[:0]
	for _, cidr := range activeExcludeRoutes {
		if !isVKExcludeCIDR(cidr) {
			filtered = append(filtered, cidr)
		}
	}
	activeExcludeRoutes = filtered
	return nil
}

func isVKExcludeCIDR(cidr string) bool {
	for _, v := range vkExcludeCIDRs {
		if v == cidr {
			return true
		}
	}
	return false
}

func resetVKExcludeRouteTracking() {
	vkExcludeRoutesMu.Lock()
	activeVKExcludeRoutes = nil
	vkExcludeRoutesMu.Unlock()
}
