//go:build windows

package desktoptun

import (
	"context"
	"net"
	"strings"
	"time"
)

// DefaultLocalIPv4 returns the LAN IPv4 on the active physical default route.
func DefaultLocalIPv4() string {
	_, ip, err := physicalIPv4Egress()
	if err != nil {
		return ""
	}
	return ip
}

// DirectBypassHosts installs /32 routes via the physical default gateway so
// traffic to resolved hosts bypasses WDTT split-default TUN routes.
func DirectBypassHosts(hosts ...string) (cleanup func(), err error) {
	gw, _, err := physicalIPv4Egress()
	if err != nil || gw == "" {
		return func() {}, err
	}
	seen := map[string]struct{}{}
	var added []string
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ips, lerr := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		cancel()
		if lerr != nil {
			continue
		}
		for _, ip := range ips {
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			addr := v4.String()
			if _, dup := seen[addr]; dup {
				continue
			}
			seen[addr] = struct{}{}
			if err := addHostRoute(addr, gw, 1); err == nil {
				added = append(added, addr)
			}
		}
	}
	return func() {
		for _, ip := range added {
			_ = deleteHostRoute(ip)
		}
	}, nil
}
