package wb1

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func TestSOCKS5EchoThroughMux(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()
	echoAddr := ln.Addr().String()

	key, err := DeriveKey("secret", "room-socks")
	if err != nil {
		t.Fatal(err)
	}
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	creator := NewMux(key, right)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()

	go ServeAccept(ctx, creator, func(dest string) (net.Conn, error) {
		d := net.Dialer{Timeout: 2 * time.Second}
		return d.DialContext(ctx, "tcp", dest)
	})

	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()
	go func() {
		_ = ServeSOCKS(ctx, socksLn, "user", "pass", func(ctx context.Context, dest string) (net.Conn, error) {
			return joiner.Dial(ctx, dest)
		})
	}()

	auth := &proxy.Auth{User: "user", Password: "pass"}
	dialer, err := proxy.SOCKS5("tcp", socksLn.Addr().String(), auth, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	c, err := dialer.Dial("tcp", echoAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo %q", buf)
	}
	cancel()
	left.Close()
	right.Close()
}

func TestSOCKS5UDPThroughMux(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := echo.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echo.WriteTo(append([]byte{'R'}, buf[:n]...), addr)
		}
	}()

	key, err := DeriveKey("secret", "room-socks-udp")
	if err != nil {
		t.Fatal(err)
	}
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	creator := NewMux(key, right)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()

	echoAddr := echo.LocalAddr().String()
	go ServeAcceptUDP(ctx, creator, func(dest string) (net.Conn, error) {
		d := net.Dialer{Timeout: 2 * time.Second}
		return d.DialContext(ctx, "tcp", dest)
	}, func(ctx context.Context, dest string) (net.Conn, error) {
		return net.Dial("udp", echoAddr)
	})

	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()
	go func() {
		_ = ServeSOCKSUDP(ctx, socksLn, "user", "pass", joiner.Dial, func(ctx context.Context, dest string) (net.Conn, error) {
			return joiner.Dial(ctx, UDPDest(dest))
		})
	}()
	time.Sleep(50 * time.Millisecond)

	hold, relayPort := socks5UDPAssociate(t, socksLn.Addr().String(), "user", "pass")
	defer hold.Close()

	client, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	pkt := encodeSocksUDPIPv4(net.IPv4(1, 2, 3, 4), 53, []byte("ping"))
	relay := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: relayPort}
	if _, err := client.WriteTo(pkt, relay); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := client.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	_, _, payload, err := parseSocksUDP(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "Rping" {
		t.Fatalf("udp echo %q", payload)
	}
	cancel()
	left.Close()
	right.Close()
}

func socks5UDPAssociate(t *testing.T, addr, user, pass string) (net.Conn, int) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[0] != 0x05 || hdr[1] != 0x02 {
		t.Fatalf("method %x", hdr)
	}
	u, p := []byte(user), []byte(pass)
	auth := []byte{0x01, byte(len(u))}
	auth = append(auth, u...)
	auth = append(auth, byte(len(p)))
	auth = append(auth, p...)
	if _, err := c.Write(auth); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatal(err)
	}
	if hdr[0] != 0x01 || hdr[1] != 0x00 {
		t.Fatalf("auth %x", hdr)
	}
	req := []byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	rep := make([]byte, 10)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatal(err)
	}
	if rep[1] != 0x00 {
		t.Fatalf("udp associate rep %x", rep)
	}
	port := int(rep[8])<<8 | int(rep[9])
	_ = c.SetDeadline(time.Time{})
	return c, port
}
