//go:build linux

package backend

import (
	"strings"
	"sync"
)

var (
	activeVKExcludeRoutes []string
	vkExcludeRoutesMu     sync.Mutex
)

func installVKExcludeRoutes(gw string) error {
	vkExcludeRoutesMu.Lock()
	defer vkExcludeRoutesMu.Unlock()
	var added []string
	for _, cidr := range vkExcludeCIDRs {
		if run("ip", "route", "add", cidr, "via", gw) == nil {
			added = append(added, cidr)
		}
	}
	activeVKExcludeRoutes = append(activeVKExcludeRoutes, added...)
	activeRoutesMu.Lock()
	activeRoutes = append(activeRoutes, added...)
	activeRoutesMu.Unlock()
	return nil
}

func uninstallVKExcludeRoutes() error {
	vkExcludeRoutesMu.Lock()
	routes := append([]string(nil), activeVKExcludeRoutes...)
	activeVKExcludeRoutes = nil
	vkExcludeRoutesMu.Unlock()
	for _, cidr := range routes {
		_ = run("ip", "route", "del", cidr)
	}
	activeRoutesMu.Lock()
	filtered := activeRoutes[:0]
	for _, entry := range activeRoutes {
		if strings.HasPrefix(entry, "dev:") {
			filtered = append(filtered, entry)
			continue
		}
		if isVKExcludeCIDR(entry) {
			continue
		}
		filtered = append(filtered, entry)
	}
	activeRoutes = filtered
	activeRoutesMu.Unlock()
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
