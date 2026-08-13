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
