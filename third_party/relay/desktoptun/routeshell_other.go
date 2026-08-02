//go:build !windows

package desktoptun

import (
	"errors"
	"net"
)

// RouteShell is a no-op outside Windows (xray TUN is Windows-only for WB desktop).
type RouteShell struct{}

func NewRouteShell(string, func(string, ...any)) (*RouteShell, error) {
	return nil, errors.New("desktoptun: RouteShell requires windows")
}

func (r *RouteShell) Prepare() error { return nil }
func (r *RouteShell) AddBypassIP(net.IP) error {
	return errors.New("desktoptun: RouteShell requires windows")
}
func (r *RouteShell) AddBypassFromCandidate(string) error { return nil }
func (r *RouteShell) InstallSplitDefaultRoutes(string) error {
	return errors.New("desktoptun: RouteShell requires windows")
}
func (r *RouteShell) FinishTunSetup(string, string, string, int) error {
	return errors.New("desktoptun: RouteShell requires windows")
}
func (r *RouteShell) Stop()                               {}
func (r *RouteShell) EgressIface() (alias, localIP string, ifIndex uint32) { return "", "", 0 }
