package wbjrunner

import (
	"context"
	"net"
	"net/http"
	"time"
)

// dialBypassHost dials WB signaling hosts by resolved IP (iOS-style), bypassing
// the dead gVisor netstack while WDTT-WB split routes are up.
func (b *tunBypass) dialBypassHost(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
	ipStr, err := b.resolveICEHost(host)
	if err != nil {
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(ipStr, port))
}

func (b *tunBypass) signalingHTTPClient() *http.Client {
	if b == nil {
		return nil
	}
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext:           b.dialBypassHost,
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}
