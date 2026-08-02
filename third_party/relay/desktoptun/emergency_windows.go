//go:build windows

package desktoptun

// EmergencyDown tears down split-default routes and adapter DNS without a live
// Tunnel handle (e.g. stale runner still shutting down while user reconnects).
func EmergencyDown(adapterName string) {
	if adapterName == "" {
		return
	}
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := deleteRouteByPrefix(prefix, adapterName); err != nil {
			// best-effort
			_ = err
		}
	}
	_ = clearAdapterDNS(adapterName)
	restoreIPv6Bindings()
}
