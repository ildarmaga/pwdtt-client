package wbjrunner

import (
	"net"
	"testing"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
)

func TestResolveHostForceKeepsStaleOnLookupFail(t *testing.T) {
	b := newTunBypass(nil, func(string, ...any) {})
	stale := net.ParseIP("1.2.3.4").To4()
	b.mu.Lock()
	b.hosts["stream.wb.ru"] = []net.IP{stale}
	b.mu.Unlock()

	b.resolveHostForce("invalid..host")

	got := b.cachedIPs("stream.wb.ru")
	if len(got) != 1 || !got[0].Equal(stale) {
		t.Fatalf("stale cache lost: got %v want [%v]", got, stale)
	}
}

func TestResolveHostSkipsCached(t *testing.T) {
	b := newTunBypass(nil, func(string, ...any) {})
	stale := net.ParseIP("1.2.3.4").To4()
	b.mu.Lock()
	b.hosts["stream.wb.ru"] = []net.IP{stale}
	b.mu.Unlock()

	b.resolveHost("stream.wb.ru")

	got := b.cachedIPs("stream.wb.ru")
	if len(got) != 1 || !got[0].Equal(stale) {
		t.Fatalf("cached entry changed: got %v want [%v]", got, stale)
	}
}

func TestResolveHostsSkipsAllCached(t *testing.T) {
	b := newTunBypass(nil, func(string, ...any) {})
	for _, host := range common.WBBypassHosts("") {
		b.mu.Lock()
		b.hosts[host] = []net.IP{net.ParseIP("1.2.3.4").To4()}
		b.mu.Unlock()
	}
	b.resolveHosts(common.WBBypassHosts("")...)
	for _, host := range common.WBBypassHosts("") {
		got := b.cachedIPs(host)
		if len(got) == 0 {
			t.Fatalf("cache lost for %s", host)
		}
	}
}

func TestLookupHostDirectPrefersIPv4(t *testing.T) {
	ips, err := lookupIPv4Direct("one.one.one.one")
	if err != nil {
		t.Skip("direct DNS unavailable:", err)
	}
	if len(ips) == 0 {
		t.Fatal("expected at least one A record")
	}
	for _, ip := range ips {
		if ip.To4() == nil {
			t.Fatalf("expected IPv4, got %v", ip)
		}
	}
}
