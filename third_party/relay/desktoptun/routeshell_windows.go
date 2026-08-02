//go:build windows

package desktoptun

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
)

// RouteShell installs OS routes when xray owns the wintun adapter.
// Split-default routes and adapter tuning are applied here (not via xray autoRoute).
//
// LAN + wintun-gateway bypasses must match the gVisor Tunnel.Start path
// (desktoptun_windows.go): without them, Windows LAN scanners and adapter
// noise hairpin through xray→SOCKS→KCP and wedge the shared carrier.
type RouteShell struct {
	adapterName string
	log         func(string, ...any)

	mu          sync.Mutex
	prepared    bool
	bypass      map[string]struct{}
	lanInstalled bool
	origGateway string
	origIfAlias string
	origIfIP    string
	origIfIndex uint32
}

// NewRouteShell returns a bypass route manager for an xray-owned adapter.
func NewRouteShell(adapterName string, logFn func(string, ...any)) (*RouteShell, error) {
	if adapterName == "" {
		return nil, errors.New("desktoptun: AdapterName required")
	}
	if logFn == nil {
		logFn = log.Printf
	}
	return &RouteShell{
		adapterName: adapterName,
		log:         logFn,
		bypass:      make(map[string]struct{}),
	}, nil
}

// Prepare captures the default gateway used for bypass /32 routes.
func (r *RouteShell) Prepare() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prepared {
		return nil
	}
	gw, alias, localIP, ifIdx, err := defaultIPv4Egress()
	if err != nil {
		return fmt.Errorf("desktoptun: read default gateway: %w", err)
	}
	r.origGateway = gw
	r.origIfAlias = alias
	r.origIfIP = localIP
	r.origIfIndex = ifIdx
	rememberPhysicalEgress(gw, localIP)
	r.prepared = true
	r.log("[desktoptun] route shell gateway %s via %q ip=%s ifIndex=%d (adapter=%s)", gw, alias, r.origIfIP, ifIdx, r.adapterName)
	return nil
}

// EgressIface returns the physical NIC used before TUN (alias, local IPv4, ifIndex).
func (r *RouteShell) EgressIface() (alias, localIP string, ifIndex uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.origIfAlias, r.origIfIP, r.origIfIndex
}

// AddBypassIP installs a /32 route via the original default gateway.
func (r *RouteShell) AddBypassIP(ip net.IP) error {
	if ip == nil {
		return errors.New("desktoptun: nil IP")
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil
	}
	addr := v4.String()
	r.mu.Lock()
	if _, dup := r.bypass[addr]; dup {
		r.mu.Unlock()
		return nil
	}
	gw := r.origGateway
	r.mu.Unlock()
	if gw == "" {
		return errors.New("desktoptun: original gateway unknown, call Prepare first")
	}
	if err := addHostRoute(addr, gw, 1); err != nil {
		return fmt.Errorf("desktoptun: add bypass %s via %s: %w", addr, gw, err)
	}
	r.mu.Lock()
	r.bypass[addr] = struct{}{}
	r.mu.Unlock()
	r.log("[desktoptun] bypass %s -> %s", addr, gw)
	return nil
}

// AddBypassFromCandidate parses SDP candidate lines and bypasses IPv4 addresses.
func (r *RouteShell) AddBypassFromCandidate(candidate string) error {
	ips := extractCandidateIPs(candidate)
	if len(ips) == 0 {
		return nil
	}
	var firstErr error
	for _, ip := range ips {
		if err := r.AddBypassIP(ip); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// InstallSplitDefaultRoutesIdx adds split-default routes bound to the adapter
// InterfaceIndex. tunPeer is the on-link nexthop (e.g. 10.99.0.1).
func (r *RouteShell) InstallSplitDefaultRoutesIdx(idx uint32, tunPeer string) error {
	if tunPeer == "" {
		return errors.New("desktoptun: tunPeer required")
	}
	var firstErr error
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := addRouteViaIdx(prefix, idx, tunPeer, 2); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return fmt.Errorf("desktoptun: add split routes: %w", firstErr)
	}
	r.log("[desktoptun] split default routes on ifIndex=%d via %s", idx, tunPeer)
	return nil
}

// FinishTunSetup resolves the xray-created adapter by ifIndex (its alias may be
// a localized default, not WDTT-WB), assigns tunnel IP, split routes and tuning.
// Configuration is index-based so it does not depend on the adapter alias.
func (r *RouteShell) FinishTunSetup(tunIP, tunMask, tunPeer string, mtu int) error {
	idx, ok := TunAdapterIfIndex(r.adapterName)
	if !ok {
		return fmt.Errorf("desktoptun: TUN adapter not found for setup")
	}
	// Best-effort: give it the WDTT-WB alias for UI/teardown consistency.
	bestEffortRenameToWant(r.adapterName, idx)

	if err := enableAdapterIdx(idx); err != nil {
		r.log("[desktoptun] enable adapter failed (continuing): %v", err)
	}
	if tunIP != "" && tunMask != "" {
		if err := setAdapterIPIdx(idx, tunIP, maskToPrefixLen(tunMask)); err != nil {
			r.log("[desktoptun] set ip failed (continuing): %v", err)
		}
	}
	// LAN/sink BEFORE split-default: otherwise every local flow hairpins into
	// xray→SOCKS→KCP for the duration of bypass install (field: ~10s with route -p).
	r.installLocalNoiseBypass()
	if err := r.InstallSplitDefaultRoutesIdx(idx, tunPeer); err != nil {
		return err
	}
	if err := setAdapterMetricIdx(idx, 1); err != nil {
		r.log("[desktoptun] set metric failed (continuing): %v", err)
	}
	if mtu > 0 {
		if err := setAdapterMTUIdx(idx, mtu); err != nil {
			r.log("[desktoptun] set mtu failed (continuing): %v", err)
		}
	}
	if err := clearAdapterDNSIdx(idx); err != nil {
		r.log("[desktoptun] clear dns failed (continuing): %v", err)
	}
	if disabled := disableIPv6ExceptTunnel(r.adapterName); len(disabled) > 0 {
		r.log("[desktoptun] IPv6 disabled on: %v", disabled)
	}
	if !WaitAdapterUpIdx(idx, tunIP) {
		r.log("[desktoptun] warn: tun IP %s not confirmed on ifIndex=%d", tunIP, idx)
	}
	return nil
}

// installLocalNoiseBypass steers RFC1918 + the synthetic wintun gateway off the
// tunnel at the OS level (metric 1 beats split-default metric 2). Mirrors
// Tunnel.Start so the xray RouteShell path does not regress into a LAN/SOCKS storm.
func (r *RouteShell) installLocalNoiseBypass() {
	r.mu.Lock()
	gw := r.origGateway
	r.mu.Unlock()
	if gw == "" {
		r.log("[desktoptun] skip LAN bypass: gateway unknown")
		return
	}
	for _, cidr := range lanBypassCIDRs {
		if err := addBypassCIDR(cidr, gw, 1); err != nil {
			r.log("[desktoptun] add LAN bypass %s failed: %v", cidr, err)
			continue
		}
		r.log("[desktoptun] LAN bypass %s -> %s", cidr, gw)
	}
	r.mu.Lock()
	r.lanInstalled = true
	r.mu.Unlock()

	// Same sink host the joiner rejects (common/socks.go IsTunnelSinkHost).
	if err := r.AddBypassIP(net.IPv4(172, 31, 255, 254)); err != nil {
		r.log("[desktoptun] bypass wintun gateway: %v", err)
	}
}

// Stop removes installed bypass routes.
func (r *RouteShell) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ip := range r.bypass {
		if err := deleteHostRoute(ip); err != nil {
			r.log("[desktoptun] remove bypass route %s: %v", ip, err)
		}
	}
	r.bypass = make(map[string]struct{})
	if r.lanInstalled {
		for _, cidr := range lanBypassCIDRs {
			if err := deleteBypassCIDR(cidr); err != nil {
				r.log("[desktoptun] remove LAN bypass %s: %v", cidr, err)
			}
		}
		r.lanInstalled = false
	}
	r.prepared = false
	r.origGateway = ""
	forgetPhysicalEgress()
}
