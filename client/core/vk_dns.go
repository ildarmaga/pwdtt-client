package core

import (
	"context"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"golang.org/x/net/proxy"
)

// Static VK host IPs (same idea as vk-turn-proxy-ios pre-resolved vk_host_ips).
// Used when public DNS UDP is blocked but direct TCP to VK CDN works.
var vkStaticHostIPs = map[string][]string{
	"api.vk.me": {
		"87.240.137.130", "87.240.139.193", "87.240.190.75", "93.186.225.205",
		"87.240.137.206", "87.240.129.140", "87.240.137.207", "87.240.190.70", "87.240.137.208",
	},
	"api.vk.ru": {
		"87.240.129.140", "87.240.137.206", "87.240.137.207", "87.240.139.193",
		"87.240.190.70", "87.240.190.75", "87.240.137.130", "87.240.137.208", "93.186.225.205",
	},
	"login.vk.ru": {"93.186.237.1", "95.213.56.1"},
	"id.vk.ru":    {"93.186.237.1", "95.213.56.1"},
}

var vkRuntimeHostIPs sync.Map // host -> []string, filled by resolveVKHostsOnce

type vkAwareDialer struct {
	inner net.Dialer
}

func (d *vkAwareDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

func (d *vkAwareDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return d.inner.DialContext(ctx, network, addr)
	}
	ips := vkDialIPs(host)
	if len(ips) == 0 {
		return d.inner.DialContext(ctx, network, addr)
	}
	var lastErr error
	for _, ip := range ips {
		target := net.JoinHostPort(ip, port)
		conn, dialErr := d.inner.DialContext(ctx, network, target)
		if dialErr == nil {
			log.Printf("[vk-dns] %s -> %s", host, ip)
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return d.inner.DialContext(ctx, network, addr)
}

func vkDialIPs(host string) []string {
	host = strings.ToLower(strings.TrimSpace(host))
	if v, ok := vkRuntimeHostIPs.Load(host); ok {
		if ips, ok := v.([]string); ok && len(ips) > 0 {
			return ips
		}
	}
	return vkStaticHostIPs[host]
}

func resolveVKHostsOnce() {
	hosts := []string{"api.vk.me", "api.vk.ru", "login.vk.ru", "calls.okcdn.ru", "vk.com"}
	r := &net.Resolver{PreferGo: false} // OS DNS (router) — same path curl uses on Windows
	for _, host := range hosts {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ips, err := r.LookupHost(ctx, host)
		cancel()
		if err != nil || len(ips) == 0 {
			log.Printf("[vk-dns] system resolve %s: %v (using static fallback if any)", host, err)
			continue
		}
		vkRuntimeHostIPs.Store(host, ips)
		log.Printf("[vk-dns] system resolve %s -> %v", host, ips)
	}
}

func newVKHTTPClient(jar ...fhttp.CookieJar) (tlsclient.HttpClient, error) {
	resolveVKHostsOnce()
	dialer := &vkAwareDialer{
		inner: net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
			Resolver:  &net.Resolver{PreferGo: false},
		},
	}
	opts := []tlsclient.HttpClientOption{
		tlsclient.WithTimeoutSeconds(20),
		tlsclient.WithClientProfile(profiles.Chrome_146),
		tlsclient.WithProxyDialerFactory(func(_ string, _ time.Duration, _ *net.TCPAddr, _ fhttp.Header, _ tlsclient.Logger) (proxy.ContextDialer, error) {
			return dialer, nil
		}),
	}
	if len(jar) > 0 && jar[0] != nil {
		opts = append(opts, tlsclient.WithCookieJar(jar[0]))
	}
	return tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), opts...)
}
