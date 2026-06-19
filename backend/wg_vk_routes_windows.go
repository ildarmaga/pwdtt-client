//go:build windows

package backend

import "sync"

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

// delVKRoutes снимает прямые маршруты cidrs и убирает их из учёта.
func delVKRoutes(cidrs []string) error {
	drop := make(map[string]bool, len(cidrs))
	for _, c := range cidrs {
		drop[c] = true
	}
	vkExcludeRoutesMu.Lock()
	activeVKExcludeRoutes = filterOutStrings(activeVKExcludeRoutes, drop)
	vkExcludeRoutesMu.Unlock()
	for _, cidr := range cidrs {
		ip, _, _ := parseCIDR(cidr)
		if ip != "" {
			_ = run("route", "delete", ip)
		}
	}
	filtered := activeExcludeRoutes[:0]
	for _, cidr := range activeExcludeRoutes {
		if !drop[cidr] {
			filtered = append(filtered, cidr)
		}
	}
	activeExcludeRoutes = filtered
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
