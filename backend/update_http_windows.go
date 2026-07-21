//go:build windows

package backend

import (
	"net"
	"net/http"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/desktoptun"
)

var updateBypassHosts = []string{
	"api.github.com",
	"github.com",
	"release-assets.githubusercontent.com",
	"objects.githubusercontent.com",
}

// withUpdateDirectEgress installs /32 routes past WDTT TUN so GitHub is
// reachable on the LAN path. Only use when the tunnel is OFF — while VPN is
// up, GitHub is often blocked on ISP and must go through the tunnel instead
// (same path as the system browser).
func withUpdateDirectEgress(extraHosts ...string) func() {
	hosts := append(append([]string(nil), updateBypassHosts...), extraHosts...)
	cleanup, err := desktoptun.DirectBypassHosts(hosts...)
	if err != nil {
		return func() {}
	}
	return cleanup
}

// newUpdateHTTPClient builds the updater client.
// viaTunnel=true: bind nothing special — traffic follows the VPN default route.
// viaTunnel=false: bind LAN IP so we do not accidentally egress via a stale TUN.
func newUpdateHTTPClient(timeout time.Duration, viaTunnel bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   45 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if !viaTunnel {
		if ip := desktoptun.DefaultLocalIPv4(); ip != "" {
			if parsed := net.ParseIP(ip); parsed != nil {
				dialer.LocalAddr = &net.TCPAddr{IP: parsed}
			}
		}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// Never use system/v2rayN HTTP_PROXY — that can loop or blackhole.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   90 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ExpectContinueTimeout: 5 * time.Second,
			MaxIdleConns:          4,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}
