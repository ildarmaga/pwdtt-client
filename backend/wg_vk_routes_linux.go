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

func installVKExcludeRoutes(gw string) error { return addVKRoutes(gw, vkExcludeCIDRs) }

func uninstallVKExcludeRoutes() error { return delVKRoutes(vkExcludeCIDRs) }

// addVKRoutes ставит прямые маршруты для cidrs через шлюз gw.
func addVKRoutes(gw string, cidrs []string) error {
	vkExcludeRoutesMu.Lock()
	defer vkExcludeRoutesMu.Unlock()
	var added []string
	for _, cidr := range cidrs {
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

// delVKRoutes снимает прямые маршруты cidrs и убирает их из учёта.
func delVKRoutes(cidrs []string) error {
	drop := make(map[string]bool, len(cidrs))
	for _, c := range cidrs {
		drop[c] = true
	}
	for _, cidr := range cidrs {
		_ = run("ip", "route", "del", cidr)
	}
	vkExcludeRoutesMu.Lock()
	activeVKExcludeRoutes = filterOutStrings(activeVKExcludeRoutes, drop)
	vkExcludeRoutesMu.Unlock()
	activeRoutesMu.Lock()
	kept := activeRoutes[:0]
	for _, entry := range activeRoutes {
		if strings.HasPrefix(entry, "dev:") || !drop[entry] {
			kept = append(kept, entry)
		}
	}
	activeRoutes = kept
	activeRoutesMu.Unlock()
	return nil
}

func filterOutStrings(in []string, drop map[string]bool) []string {
	out := in[:0]
	for _, s := range in {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}

func resetVKExcludeRouteTracking() {
	vkExcludeRoutesMu.Lock()
	activeVKExcludeRoutes = nil
	vkExcludeRoutesMu.Unlock()
}
