//go:build windows

package desktoptun

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
)

// Config describes one wintun adapter and the SOCKS5 endpoint that
// tun2socks should forward traffic to.
type Config struct {
	// AdapterName is the wintun adapter name shown in Windows. Must be unique.
	AdapterName string
	// TunnelIP is the local address assigned to the adapter (e.g. 10.99.0.2).
	TunnelIP string
	// TunnelMask is the subnet mask as dotted-quad (e.g. 255.255.255.0).
	TunnelMask string
	// TunnelPeer is the on-link next-hop used by the default routes
	// installed on the wintun adapter (e.g. 10.99.0.1). It does not need
	// to answer; wintun is point-to-point so Windows does not ARP for it.
	TunnelPeer string
	// MTU for the wintun adapter. 1500 is fine for most networks.
	MTU int
	// DNSServers is the list of resolvers to assign to the adapter.
	DNSServers []string
	// SocksHost / SocksPort / SocksUser / SocksPass: the local SOCKS5
	// proxy that the headless joiner is running. tun2socks dials this
	// for every captured TCP/UDP flow.
	SocksHost string
	SocksPort int
	SocksUser string
	SocksPass string
	// Dialer, when set, routes captured flows straight into the overlay
	// tunnel via an in-app netstack (no SOCKS5 loopback). When nil the
	// legacy tun2socks → SOCKS5 path is used.
	Dialer TunnelDialer
	LogFn  func(format string, args ...any)
}

// Tunnel owns one wintun adapter and the tun2socks engine instance
// driving it. Call New + Start to bring it up, Stop to tear it down.
type Tunnel struct {
	cfg Config
	log func(string, ...any)

	mu          sync.Mutex
	started     bool
	stopped     bool
	bypass      map[string]struct{} // /32 routes we installed for bypass
	origGateway string
	origIfAlias string
	tunIfIndex  uint32
	closeEngine func()
}

// New validates a Config and returns a Tunnel ready to Start.
func New(cfg Config) (*Tunnel, error) {
	if cfg.AdapterName == "" {
		return nil, errors.New("desktoptun: AdapterName required")
	}
	if cfg.TunnelIP == "" || cfg.TunnelMask == "" || cfg.TunnelPeer == "" {
		return nil, errors.New("desktoptun: TunnelIP, TunnelMask and TunnelPeer required")
	}
	if cfg.MTU <= 0 {
		cfg.MTU = 1500
	}
	if cfg.SocksHost == "" {
		cfg.SocksHost = common.SocksLocalhostIP
	}
	if cfg.SocksPort <= 0 && cfg.Dialer == nil {
		return nil, errors.New("desktoptun: SocksPort required")
	}
	if cfg.LogFn == nil {
		cfg.LogFn = log.Printf
	}
	// Netstack/VK path: leave DNSServers empty — Windows uses router DNS on Wi‑Fi.
	if len(cfg.DNSServers) == 0 && cfg.Dialer == nil {
		cfg.DNSServers = []string{"1.1.1.1", "8.8.8.8"}
	}
	return &Tunnel{
		cfg:    cfg,
		log:    cfg.LogFn,
		bypass: make(map[string]struct{}),
	}, nil
}

// Start brings up the wintun adapter, captures the original default
// gateway so subsequent AddBypass* calls can pin /32 routes to it,
// configures the adapter, installs the split default route through
// the tunnel, and starts the tun2socks engine. After Start returns,
// every packet leaving the OS that does not match a bypass route
// will be steered through SOCKS5.
func (t *Tunnel) Start() error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("desktoptun: already started")
	}
	t.mu.Unlock()

	gw, alias, localIP, _, err := physicalIPv4EgressFull()
	if err != nil {
		return fmt.Errorf("desktoptun: read default gateway: %w", err)
	}
	t.mu.Lock()
	t.origGateway = gw
	t.origIfAlias = alias
	t.mu.Unlock()
	rememberPhysicalEgress(gw, localIP)
	t.log("[desktoptun] original default gateway %s via %q", gw, alias)

	closeEngine, err := t.startTun2socks()
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.closeEngine = closeEngine
	t.mu.Unlock()

	// Never hold t.mu across netsh/route subprocesses or AddBypassIP — WebRTC
	// callbacks install bypass routes concurrently and Start used to deadlock.
	if err := setAdapterIP(t.cfg.AdapterName, t.cfg.TunnelIP, t.cfg.TunnelMask); err != nil {
		closeEngine()
		return fmt.Errorf("desktoptun: set adapter ip: %w", err)
	}
	if err := setAdapterMTU(t.cfg.AdapterName, t.cfg.MTU); err != nil {
		t.log("[desktoptun] set mtu failed (continuing): %v", err)
	}
	// DNS stays on physical NIC (router 192.168.x.1 via LAN bypass). Public
	// resolvers on 1.1.1.1/8.8.8.8 are bypassed so Windows uses ISP/router DNS
	// (needed for RU mirror domains like *.lordfilms.ru). TCP still via tunnel.
	if err := clearAdapterDNS(t.cfg.AdapterName); err != nil {
		t.log("[desktoptun] clear dns failed (continuing): %v", err)
	}
	if t.cfg.Dialer != nil {
		t.installPublicResolverBypass()
	} else if len(t.cfg.DNSServers) > 0 {
		if err := setAdapterDNS(t.cfg.AdapterName, t.cfg.DNSServers); err != nil {
			t.log("[desktoptun] set dns failed (continuing): %v", err)
		}
	}
	if err := setAdapterMetric(t.cfg.AdapterName, 1); err != nil {
		t.log("[desktoptun] set metric failed (continuing): %v", err)
	}
	if disabled := disableIPv6ExceptTunnel(t.cfg.AdapterName); len(disabled) > 0 {
		t.log("[desktoptun] IPv6 disabled on: %v", disabled)
	}
	idx, err := adapterIPv4Index(t.cfg.AdapterName)
	if err == nil {
		t.mu.Lock()
		t.tunIfIndex = idx
		t.mu.Unlock()
	}

	// LAN/sink BEFORE split-default so local noise never hairpins into the tunnel.
	for _, cidr := range lanBypassCIDRs {
		if err := addBypassCIDR(cidr, gw, 1); err != nil {
			t.log("[desktoptun] add LAN bypass %s failed: %v", cidr, err)
		} else {
			t.log("[desktoptun] LAN bypass %s -> %s", cidr, gw)
		}
	}
	if err := addHostRoute("172.31.255.254", gw, 1); err != nil {
		t.log("[desktoptun] bypass wintun gateway: %v", err)
	} else {
		t.mu.Lock()
		t.bypass["172.31.255.254"] = struct{}{}
		t.mu.Unlock()
	}

	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := addRouteViaAdapter(prefix, t.cfg.AdapterName, t.cfg.TunnelPeer, 2); err != nil {
			t.log("[desktoptun] add default-half %s failed: %v", prefix, err)
		}
	}

	t.mu.Lock()
	t.started = true
	t.mu.Unlock()
	t.log("[desktoptun] up: adapter=%s ip=%s/%s peer=%s",
		t.cfg.AdapterName, t.cfg.TunnelIP, t.cfg.TunnelMask, t.cfg.TunnelPeer)
	return nil
}

// Stop removes every route we installed and shuts the wintun adapter
// down. Safe to call from multiple goroutines and idempotent.
func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started || t.stopped {
		return
	}
	t.stopped = true

	for ip := range t.bypass {
		if err := deleteHostRoute(ip); err != nil {
			t.log("[desktoptun] remove bypass route %s: %v", ip, err)
		}
	}
	t.bypass = nil

	for _, cidr := range lanBypassCIDRs {
		if err := deleteBypassCIDR(cidr); err != nil {
			t.log("[desktoptun] remove LAN bypass %s: %v", cidr, err)
		}
	}

	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := deleteRouteByPrefix(prefix, t.cfg.AdapterName); err != nil {
			t.log("[desktoptun] remove default-half %s: %v", prefix, err)
		}
	}

	if err := clearAdapterDNS(t.cfg.AdapterName); err != nil {
		t.log("[desktoptun] clear dns: %v", err)
	}
	restoreIPv6Bindings()

	if t.closeEngine != nil {
		t.closeEngine()
	}
	forgetPhysicalEgress()
	t.log("[desktoptun] down")
}

// AddBypassIP installs a /32 route for ip via the original default
// gateway. Used so the joiner's own connections to signaling and SFU
// hosts skip the tunnel and reach the real network directly.
func (t *Tunnel) AddBypassIP(ip net.IP) error {
	if ip == nil {
		return errors.New("desktoptun: nil IP")
	}
	v4 := ip.To4()
	if v4 == nil {
		// IPv6 bypass is out of scope: the joiner negotiates v4 hosts
		return nil
	}
	addr := v4.String()
	t.mu.Lock()
	if _, dup := t.bypass[addr]; dup {
		t.mu.Unlock()
		return nil
	}
	gw := t.origGateway
	t.mu.Unlock()
	if gw == "" {
		return errors.New("desktoptun: original gateway unknown, start the tunnel first")
	}
	if err := addHostRoute(addr, gw, 1); err != nil {
		return fmt.Errorf("desktoptun: add bypass %s via %s: %w", addr, gw, err)
	}
	t.mu.Lock()
	t.bypass[addr] = struct{}{}
	t.mu.Unlock()
	t.log("[desktoptun] bypass %s -> %s", addr, gw)
	return nil
}

// RemoveBypassIP deletes a /32 bypass route previously installed by AddBypassIP.
func (t *Tunnel) RemoveBypassIP(ip net.IP) error {
	if ip == nil {
		return nil
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil
	}
	addr := v4.String()
	t.mu.Lock()
	if _, ok := t.bypass[addr]; !ok {
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()
	if err := deleteHostRoute(addr); err != nil {
		return fmt.Errorf("desktoptun: remove bypass %s: %w", addr, err)
	}
	t.mu.Lock()
	delete(t.bypass, addr)
	t.mu.Unlock()
	t.log("[desktoptun] bypass removed %s", addr)
	return nil
}

// AddBypassHost resolves hostname (system resolver, no tunnel) and
// installs a /32 bypass route for every returned A record. The
// resolved IPs are returned so callers can wire them into other
// settings, e.g. a Pion ResolveICEHost callback.
func (t *Tunnel) AddBypassHost(hostname string) ([]net.IP, error) {
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, t.AddBypassIP(ip)
	}
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil, fmt.Errorf("desktoptun: resolve %s: %w", hostname, err)
	}
	out := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			if addErr := t.AddBypassIP(v4); addErr != nil {
				t.log("[desktoptun] bypass host %s ip %s: %v", hostname, v4, addErr)
				continue
			}
			out = append(out, v4)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("desktoptun: no v4 addresses for %s", hostname)
	}
	return out, nil
}

// AddBypassFromCandidate parses an SDP candidate line ("candidate:..."
// or "a=candidate:...") and bypasses every IPv4 address found in it.
// Both the relay address and the rel-addr (TURN reflexive) are honored.
func (t *Tunnel) AddBypassFromCandidate(candidate string) error {
	ips := extractCandidateIPs(candidate)
	if len(ips) == 0 {
		return nil
	}
	var firstErr error
	for _, ip := range ips {
		if err := t.AddBypassIP(ip); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
