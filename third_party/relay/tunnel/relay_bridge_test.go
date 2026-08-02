package tunnel

import (
	"net"
	"testing"
	"time"
)

type stubTunnel struct {
	onData  func([]byte)
	onClose func()
}

func (s *stubTunnel) SendData([]byte)                      {}
func (s *stubTunnel) SetOnData(fn func([]byte))            { s.onData = fn }
func (s *stubTunnel) SetOnClose(fn func())                 { s.onClose = fn }
func (s *stubTunnel) Reconfigure(int, int)                 {}

func TestSwapTunnelKeepConnsPreservesTCP(t *testing.T) {
	t1 := &stubTunnel{}
	rb := NewRelayBridge(t1, "video", 4096, func(string, ...any) {})

	c1, c2 := net.Pipe()
	defer c2.Close()
	rb.conns.Store(uint32(7), c1)

	t2 := &stubTunnel{}
	rb.SwapTunnelKeepConns(t2, false)

	if rb.countTCP() != 1 {
		t.Fatalf("tcp sessions=%d want 1", rb.countTCP())
	}
	if _, ok := rb.conns.Load(uint32(7)); !ok {
		t.Fatal("conn 7 was dropped")
	}
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4)
		_, _ = c1.Read(buf)
		close(done)
	}()
	if _, err := c2.Write([]byte("ping")); err != nil {
		t.Fatalf("write through pipe: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("read timed out — conn should stay open")
	}
}

func TestSwapTunnelResetsConnections(t *testing.T) {
	t1 := &stubTunnel{}
	rb := NewRelayBridge(t1, "video", 4096, func(string, ...any) {})

	c1, c2 := net.Pipe()
	rb.conns.Store(uint32(1), c1)

	t2 := &stubTunnel{}
	rb.SwapTunnelKeepConns(t2, true)

	if rb.countTCP() != 0 {
		t.Fatalf("tcp sessions=%d want 0 after reset", rb.countTCP())
	}
	buf := make([]byte, 4)
	if _, err := c2.Read(buf); err == nil {
		t.Fatal("expected closed pipe after closeAll")
	}
	_ = c2.Close()
}
