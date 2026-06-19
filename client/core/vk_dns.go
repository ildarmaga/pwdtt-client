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
	"login.vk.com": {"93.186.237.1", "95.213.56.1", "87.240.137.130", "95.213.0.1"},
	"id.vk.ru":      {"93.186.237.1", "95.213.56.1"},
	"id.vk.com":     {"93.186.237.1", "95.213.56.1"},
	"queuev4.vk.com": {"93.186.237.6", "93.186.237.7", "93.186.237.16", "95.213.56.3", "95.213.56.4"},
	"queuev4.vk.ru":  {"93.186.237.6", "93.186.237.7", "95.213.56.3", "95.213.56.4"},
	"eh.vk.com":      {"93.186.237.6", "93.186.237.7", "95.213.56.2", "95.213.56.3", "95.213.56.4"},
	"st4-9.vk.com":   {"95.142.203.40"},
	"vk.com":        {"87.240.137.130", "87.240.139.193", "93.186.225.205", "87.240.190.75"},
	"m.vk.com":      {"87.240.137.130", "87.240.139.193"},
	"oauth.vk.com":  {"87.240.137.130", "87.240.139.193"},
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
	hosts := []string{
		"api.vk.me", "api.vk.ru", "login.vk.ru", "login.vk.com", "id.vk.com",
		"queuev4.vk.com", "queuev4.vk.ru", "eh.vk.com", "calls.okcdn.ru", "vk.com",
	}
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
	return newVKHTTPClientOpts(false, jar...)
}

// NewVKHTTPClient — Chrome TLS + VK-aware dialer (OS DNS + static IP fallback).
func NewVKHTTPClient(jar ...fhttp.CookieJar) (tlsclient.HttpClient, error) {
	return newVKHTTPClientOpts(false, jar...)
}

// NewVKHTTPClientForProxy — для VK login proxy: без auto-redirect, длиннее timeout.
func NewVKHTTPClientForProxy(jar fhttp.CookieJar) (tlsclient.HttpClient, error) {
	c, err := newVKHTTPClientOpts(true, jar)
	return c, err
}

func newVKHTTPClientOpts(proxyMode bool, jar ...fhttp.CookieJar) (tlsclient.HttpClient, error) {
	resolveVKHostsOnce()
	dialer := &vkAwareDialer{
		inner: net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
			Resolver:  &net.Resolver{PreferGo: false},
		},
	}
	timeout := 20
	if proxyMode {
		timeout = 45
	}
	opts := []tlsclient.HttpClientOption{
		tlsclient.WithTimeoutSeconds(timeout),
		tlsclient.WithClientProfile(profiles.Chrome_146),
		tlsclient.WithProxyDialerFactory(func(_ string, _ time.Duration, _ *net.TCPAddr, _ fhttp.Header, _ tlsclient.Logger) (proxy.ContextDialer, error) {
			return dialer, nil
		}),
	}
	if proxyMode {
		opts = append(opts, tlsclient.WithNotFollowRedirects())
		opts = append(opts, tlsclient.WithTransportOptions(&tlsclient.TransportOptions{
			DisableCompression: true,
		}))
	}
	if len(jar) > 0 && jar[0] != nil {
		opts = append(opts, tlsclient.WithCookieJar(jar[0]))
	}
	return tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), opts...)
}
