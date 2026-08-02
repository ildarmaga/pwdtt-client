package wbjrunner

import (
	"context"
	"net"
	"time"
)

// public resolvers reached via OS routes (bypass IPs installed before TUN default route).
var publicDNSResolvers = []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}

// lookupIPv4Direct resolves A records without the system resolver. When the VPN
// tunnel is up but dead, net.LookupIP often fails (no such host) because queries
// go through the broken netstack — we need stream.wb.ru IPs for bypass routes.
func lookupIPv4Direct(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	var lastErr error
	for _, addr := range publicDNSResolvers {
		r := net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 3 * time.Second}
				return d.DialContext(ctx, "udp", addr)
			},
		}
		ips, err := r.LookupIP(ctx, "ip4", host)
		if err != nil {
			lastErr = err
			continue
		}
		var v4s []net.IP
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				v4s = append(v4s, v4)
			}
		}
		if len(v4s) > 0 {
			return v4s, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, net.ErrClosed
}
