package wbjrunner

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/desktoptun"
)

func warmupTunnel(d desktoptun.TunnelDialer, logFn func(string, ...any), onStatus func(string)) {
	start := time.Now()
	deadline := start.Add(12 * time.Second)

	var lastErr error
	for time.Now().Before(deadline) {
		client := httpClientOverDial(func(ctx context.Context, network, address string) (net.Conn, error) {
			host, portStr, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return nil, err
			}
			return d.DialTCP(ctx, host, port)
		})
		body, err := probeIPify(client)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		elapsed := time.Since(start).Round(time.Millisecond)
		logFn("[warmup] joiner ready in %s ip=%s", elapsed, body)
		probeOSRoute(logFn)
		if onStatus != nil {
			onStatus("TRAFFIC_READY")
		}
		return
	}
	logFn("[warmup] probe timeout after %s: %v", time.Since(start).Round(time.Millisecond), lastErr)
	if onStatus != nil {
		onStatus("WARMUP_FAILED")
	}
}

// warmupXrayVPN probes egress through the OS routing table (xray TUN + autoRoute).
// directEgressIP is the pre-tun public IP; matching it means routes are not through xray yet.
func warmupXrayVPN(logFn func(string, ...any), onStatus func(string), directEgressIP string) {
	start := time.Now()
	deadline := start.Add(12 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		body, err := probeOSRouteEgress()
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if directEgressIP != "" && body == directEgressIP {
			logFn("[warmup] egress still direct ip=%s (await xray route)", body)
			lastErr = fmt.Errorf("egress still direct")
			time.Sleep(500 * time.Millisecond)
			continue
		}
		elapsed := time.Since(start).Round(time.Millisecond)
		logFn("[warmup] xray VPN ready in %s ip=%s (proxy path via ipify)", elapsed, body)
		if onStatus != nil {
			onStatus("TRAFFIC_READY")
		}
		return
	}
	logFn("[warmup] xray probe timeout after %s: %v", time.Since(start).Round(time.Millisecond), lastErr)
	if onStatus != nil {
		onStatus("WARMUP_FAILED")
	}
}

// probeOSRoute checks browser-style egress: system DNS + TCP via wintun/xray.
func probeOSRoute(logFn func(string, ...any)) {
	body, err := probeOSRouteEgress()
	if err != nil {
		logFn("[warmup] OS route probe failed: %v", err)
		return
	}
	logFn("[warmup] OS route ip=%s", body)
}

func probeOSRouteEgress() (string, error) {
	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
				if err != nil || len(ips) == 0 {
					return nil, fmt.Errorf("resolve %s: %w", host, err)
				}
				d := net.Dialer{}
				return d.DialContext(ctx, "tcp4", net.JoinHostPort(ips[0].String(), port))
			},
		},
	}
	return probeIPify(client)
}

func httpClientOverDial(dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:       dial,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
		},
		Timeout: 12 * time.Second,
	}
}

func probeIPify(client *http.Client) (string, error) {
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(body), nil
}
