package wbjrunner

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/livekit"
)

type bypassRouter interface {
	AddBypassIP(ip net.IP) error
	AddBypassFromCandidate(candidate string) error
}

type tunBypass struct {
	router bypassRouter
	logFn  func(string, ...any)
	mu     sync.Mutex
	hosts  map[string][]net.IP
	pending []string
	tunUp  bool
}

func newTunBypass(router bypassRouter, logFn func(string, ...any)) *tunBypass {
	return &tunBypass{router: router, logFn: logFn, hosts: make(map[string][]net.IP)}
}

func (b *tunBypass) lookupHost(host string) ([]net.IP, error) {
	if ips, err := lookupIPv4Direct(host); err == nil && len(ips) > 0 {
		return ips, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	var v4s []net.IP
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			v4s = append(v4s, v4)
		}
	}
	if len(v4s) == 0 {
		return nil, fmt.Errorf("no A record for %s", host)
	}
	return v4s, nil
}

func (b *tunBypass) cachedIPs(host string) []net.IP {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]net.IP(nil), b.hosts[host]...)
}

func ipsEqual(a, b []net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func (b *tunBypass) storeHostIPs(host string, v4s []net.IP) {
	b.mu.Lock()
	prev := b.hosts[host]
	changed := !ipsEqual(prev, v4s)
	if changed {
		b.hosts[host] = append([]net.IP(nil), v4s...)
	}
	up := b.tunUp
	b.mu.Unlock()
	if !changed {
		return
	}
	b.logFn("[bypass] %s -> %v (pre-tun=%v)", host, v4s, !up)
	if up && b.router != nil {
		for _, ip := range v4s {
			if err := b.router.AddBypassIP(ip); err != nil {
				b.logFn("[bypass] %s %s: %v", host, ip, err)
			}
		}
	}
}

func (b *tunBypass) resolveHost(host string) {
	if b == nil || host == "" || net.ParseIP(host) != nil {
		return
	}
	b.mu.Lock()
	if _, ok := b.hosts[host]; ok {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	v4s, err := b.lookupHost(host)
	if err != nil {
		b.logFn("[bypass] resolve %s: %v", host, err)
		return
	}
	b.storeHostIPs(host, v4s)
}

// resolveHostForce re-resolves host; returns true when IPs changed. Silent when unchanged.
func (b *tunBypass) resolveHostForce(host string) bool {
	if b == nil || host == "" {
		return false
	}
	stale := b.cachedIPs(host)
	v4s, err := b.lookupHost(host)
	if err != nil || len(v4s) == 0 {
		if len(stale) > 0 {
			return false
		}
		if err != nil {
			b.logFn("[bypass] resolve %s: %v", host, err)
		}
		return false
	}
	if ipsEqual(stale, v4s) {
		return false
	}
	b.storeHostIPs(host, v4s)
	return true
}

func (b *tunBypass) resolveHosts(hosts ...string) {
	for _, h := range hosts {
		b.resolveHost(h)
	}
}

// ensureHosts resolves signaling/STUN hosts once; force=true re-checks DNS (recovery).
func (b *tunBypass) ensureHosts(serverURL string, force bool) bool {
	if b == nil {
		return false
	}
	changed := false
	for _, h := range common.WBBypassHosts(serverURL) {
		if force {
			if b.resolveHostForce(h) {
				changed = true
			}
			continue
		}
		if len(b.cachedIPs(h)) > 0 {
			continue
		}
		b.resolveHost(h)
		if len(b.cachedIPs(h)) > 0 {
			changed = true
		}
	}
	return changed
}

func (b *tunBypass) onJoin(join livekit.JoinResponse) {
	var urls []string
	for _, s := range join.ICEServers {
		urls = append(urls, s.URLs...)
	}
	b.resolveHosts(common.ICEHostsFromURLs(urls)...)
}

func (b *tunBypass) resolveICEHost(host string) (string, error) {
	b.resolveHost(host)
	b.mu.Lock()
	defer b.mu.Unlock()
	ips, ok := b.hosts[host]
	if !ok || len(ips) == 0 {
		return "", fmt.Errorf("no A record for %s", host)
	}
	return ips[0].String(), nil
}

func (b *tunBypass) noteCandidate(candidateOrSDP string) {
	if b == nil || b.router == nil {
		return
	}
	b.mu.Lock()
	up := b.tunUp
	if !up {
		b.pending = append(b.pending, candidateOrSDP)
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	_ = b.router.AddBypassFromCandidate(candidateOrSDP)
}

func (b *tunBypass) noteRemoteCandidate(_ int, candidateOrSDP string) {
	b.noteCandidate(candidateOrSDP)
	if strings.Contains(candidateOrSDP, "a=candidate:") {
		for _, line := range strings.Split(candidateOrSDP, "\n") {
			line = strings.TrimRight(line, "\r")
			if strings.HasPrefix(line, "a=candidate:") {
				b.noteCandidate(line)
			}
		}
	}
}

func (b *tunBypass) bypassIP(ip net.IP) {
	if b == nil || ip == nil || b.router == nil {
		return
	}
	b.mu.Lock()
	up := b.tunUp
	router := b.router
	b.mu.Unlock()
	if !up {
		return
	}
	if err := router.AddBypassIP(ip); err != nil {
		b.logFn("[bypass] ip %s: %v", ip, err)
	}
}

// resolverBypassIPs — public DNS resolvers: keep on physical NIC (VK-style).
// System DNS resolves hostnames; TCP/UDP to site IPs goes through the tunnel.
var resolverBypassIPs = []string{"1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9"}

func (b *tunBypass) bypassResolverIPs() {
	if b == nil {
		return
	}
	for _, ipStr := range resolverBypassIPs {
		if ip := net.ParseIP(ipStr); ip != nil {
			b.bypassIP(ip.To4())
		}
	}
}

func (b *tunBypass) ensureReconnectReachable(serverURL string) bool {
	if b == nil {
		return false
	}
	b.bypassResolverIPs()
	return b.ensureHosts(serverURL, true)
}

func (b *tunBypass) installAtTunStart() {
	if b == nil || b.router == nil {
		return
	}
	b.mu.Lock()
	hosts := b.hosts
	drained := b.pending
	b.pending = nil
	b.tunUp = true
	b.mu.Unlock()

	b.bypassResolverIPs()

	for host, ips := range hosts {
		for _, ip := range ips {
			if err := b.router.AddBypassIP(ip); err != nil {
				b.logFn("[bypass] %s %s: %v", host, ip, err)
			}
		}
	}
	for _, c := range drained {
		_ = b.router.AddBypassFromCandidate(c)
	}
}
