package wbtunnel

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

// startTCPEchoServer accepts connections and echoes back everything it reads.
func startTCPEchoServer(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr()
}

// TestWBTDialTCP exercises the in-process TCP dialer (the SOCKS-free path the
// netstack uses): open a tunneled connection and verify a byte round-trip.
func TestWBTDialTCP(t *testing.T) {
	creatorTun, joinerTun := newPipePair()
	echoAddr := startTCPEchoServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	waitKCP(t)

	host, portStr, _ := net.SplitHostPort(echoAddr.String())
	port, _ := strconv.Atoi(portStr)

	conn, err := joiner.DialTCP(ctx, host, port)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	msg := []byte("hello-wbt")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := readFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("echo mismatch: got %q", buf)
	}
}

// TestWBTDialTCPRejectsSink ensures tunnel-sink/fake-dns destinations never
// enter the tunnel (this was the 172.31.255.254 connection-storm bug).
func TestWBTDialTCPRejectsSink(t *testing.T) {
	creatorTun, joinerTun := newPipePair()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	waitKCP(t)

	for _, tc := range []struct {
		host string
		port int
	}{
		{"172.31.255.254", 443},
		{"10.99.0.1", 80},
		{"198.18.0.7", 443},
	} {
		if conn, err := joiner.DialTCP(ctx, tc.host, tc.port); err == nil {
			conn.Close()
			t.Fatalf("DialTCP(%s:%d) should be rejected", tc.host, tc.port)
		}
	}
}

// TestWBTDialUDP exercises the in-process UDP PacketConn used for DNS et al.
func TestWBTDialUDP(t *testing.T) {
	creatorTun, joinerTun := newPipePair()

	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := ln.ReadFrom(buf)
			if err != nil {
				return
			}
			if n > 0 && string(buf[:n]) == "ping" {
				_, _ = ln.WriteTo([]byte("pong"), addr)
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	waitKCP(t)

	host, portStr, _ := net.SplitHostPort(ln.LocalAddr().String())
	port, _ := strconv.Atoi(portStr)

	pc, err := joiner.DialUDP(host, port)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer pc.Close()

	dst := &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	if _, err := pc.WriteTo([]byte("ping"), dst); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	_ = pc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, from, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if string(buf[:n]) != "pong" {
		t.Fatalf("udp echo mismatch: got %q", buf[:n])
	}
	if from == nil || from.String() != dst.String() {
		t.Fatalf("udp from mismatch: got %v want %v", from, dst)
	}
}

func readFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
