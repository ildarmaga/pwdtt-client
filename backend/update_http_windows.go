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

func withUpdateDirectEgress(extraHosts ...string) func() {
	hosts := append(append([]string(nil), updateBypassHosts...), extraHosts...)
	cleanup, err := desktoptun.DirectBypassHosts(hosts...)
	if err != nil {
		return func() {}
	}
	return cleanup
}

func newUpdateHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   45 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if ip := desktoptun.DefaultLocalIPv4(); ip != "" {
		if parsed := net.ParseIP(ip); parsed != nil {
			dialer.LocalAddr = &net.TCPAddr{IP: parsed}
		}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   90 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ExpectContinueTimeout: 5 * time.Second,
			MaxIdleConns:          4,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}
