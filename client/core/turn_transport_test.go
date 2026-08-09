package core

import (
	"net"
	"testing"
	"time"
)

func TestDialTurnPacketConnUDPLocal(t *testing.T) {
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.LocalAddr().String()

	pkt, closer, err := dialTurnPacketConn(addr, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closer()
	if pkt == nil {
		t.Fatal("nil pkt")
	}
	_ = pkt.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
}

func TestDialTurnPacketConnTCPLocal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, acceptErr := ln.Accept()
		if acceptErr == nil {
			defer c.Close()
			buf := make([]byte, 64)
			_, _ = c.Read(buf)
		}
	}()

	pkt, closer, err := dialTurnPacketConn(ln.Addr().String(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer closer()
	if pkt == nil {
		t.Fatal("nil pkt")
	}
}
