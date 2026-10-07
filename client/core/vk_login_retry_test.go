package core

import (
	"context"
	"fmt"
	"net"
	"testing"
)

func TestRetryVKLoginTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !retryVKLoginTimeout(ctx, fmt.Errorf("request: %w", context.DeadlineExceeded)) {
		t.Fatal("timeout must retry")
	}
	if retryVKLoginTimeout(ctx, fmt.Errorf("invalid response")) {
		t.Fatal("API errors must not retry")
	}
	if retryVKLoginTimeout(ctx, nil) {
		t.Fatal("success must not retry")
	}
	cancel()
	if retryVKLoginTimeout(ctx, context.DeadlineExceeded) {
		t.Fatal("cancelled login must stop")
	}
}

func TestVKDialerRetriesWithDifferentEndpoint(t *testing.T) {
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host := "startup-retry.test"
	vkRuntimeHostIPs.Store(host, []string{"127.0.0.1", "127.0.0.2"})
	defer vkRuntimeHostIPs.Delete(host)
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	for offset, want := range []string{"127.0.0.1", "127.0.0.2"} {
		dialer := &vkAwareDialer{ipOffset: offset}
		conn, err := dialer.DialContext(context.Background(), "tcp4", net.JoinHostPort(host, port))
		if err != nil {
			t.Fatal(err)
		}
		got := conn.RemoteAddr().(*net.TCPAddr).IP.String()
		conn.Close()
		if got != want {
			t.Fatalf("attempt %d connected to %s, want %s", offset, got, want)
		}
	}
}
