package wbtunnel

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
)

// TestSocksUDPUnsolicitedDatagrams: Steam SDR pushes replies without a matching
// client send. The old request/response SOCKS UDP path dropped them.
func TestSocksUDPUnsolicitedDatagrams(t *testing.T) {
	skipIntegration(t)
	creatorTun, joinerTun := newPipePair()

	// Must NOT be loopback: IsNonRoutableHost short-circuits to local
	// request/response (one reply only) and never hits the tunnel path.
	localIP := nonLoopbackIPv4(t)
	ln, err := net.ListenPacket("udp", net.JoinHostPort(localIP.String(), "0"))
	if err != nil {
		t.Fatalf("listen udp on %s: %v", localIP, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := ln.ReadFrom(buf)
			if err != nil {
				return
			}
			if n == 0 {
				continue
			}
			for i := 0; i < 3; i++ {
				msg := []byte{byte('R'), byte('0' + i), byte(n)}
				if _, err := ln.WriteTo(msg, addr); err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logf := func(format string, args ...any) { t.Logf("[wbt] "+format, args...) }
	creator, err := NewCreator(ctx, creatorTun, "", "", "", logf, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", logf, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("socks listen: %v", err)
	}
	go func() { _ = joiner.ServeSOCKS(socksLn) }()
	t.Cleanup(func() { _ = socksLn.Close() })

	waitKCP(t)

	relayPort, hold := socksUDPAssociate(t, socksLn.Addr().String())
	t.Cleanup(func() { _ = hold.Close() })

	host, portStr, _ := net.SplitHostPort(ln.LocalAddr().String())
	dstPort, _ := strconv.Atoi(portStr)
	hdr := socksUDPHeaderIPv4(host, dstPort)

	client, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("client udp: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	relayAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)))
	if err != nil {
		t.Fatalf("relay addr: %v", err)
	}

	if _, err := client.WriteTo(append(hdr, []byte("ping")...), relayAddr); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := 0
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 2048)
	for got < 3 {
		_ = client.SetReadDeadline(deadline)
		n, _, err := client.ReadFrom(buf)
		if err != nil {
			t.Fatalf("read unsolicited #%d: %v (got %d/3) — SOCKS UDP not bidirectional", got+1, err, got)
		}
		if n < len(hdr)+2 {
			continue
		}
		if buf[0] != 0 || buf[3] != common.AtypIPv4 {
			t.Fatalf("bad socks udp header: %v", buf[:min(n, 10)])
		}
		payload := buf[len(hdr):n]
		if payload[0] != 'R' {
			t.Fatalf("unexpected payload %q", payload)
		}
		got++
	}
}

func nonLoopbackIPv4(t *testing.T) net.IP {
	t.Helper()
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		t.Skipf("no outbound udp for local ip: %v", err)
	}
	defer c.Close()
	ip := c.LocalAddr().(*net.UDPAddr).IP.To4()
	if ip == nil || ip.IsLoopback() {
		t.Skip("no non-loopback ipv4")
	}
	return ip
}
