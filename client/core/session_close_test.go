package core

import (
	"net"
	"testing"
	"time"
)

func TestObfsDirectCloseUnblocksRead(t *testing.T) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	conn := &obfsDirectConn{relay: pc, peer: pc.LocalAddr()}
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 64)
		_, _ = conn.Read(buf)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Read stayed blocked after Close")
	}
}
