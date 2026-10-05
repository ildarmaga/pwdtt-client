package backend

import (
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun"
)

// idleTun blocks in Read until Close, like wintun with no packets.
type idleTun struct {
	wake  chan struct{}
	once  sync.Once
	reads atomic.Int32
	ev    chan tun.Event
}

func newIdleTun() *idleTun {
	return &idleTun{wake: make(chan struct{}), ev: make(chan tun.Event)}
}

func (t *idleTun) File() *os.File           { return nil }
func (t *idleTun) MTU() (int, error)        { return 1280, nil }
func (t *idleTun) Name() (string, error)    { return "idle", nil }
func (t *idleTun) Events() <-chan tun.Event { return t.ev }
func (t *idleTun) BatchSize() int           { return 1 }
func (t *idleTun) Write(bufs [][]byte, offset int) (int, error) {
	return len(bufs), nil
}

func (t *idleTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	t.reads.Add(1)
	<-t.wake
	return 0, os.ErrClosed
}

func (t *idleTun) Close() error {
	t.once.Do(func() { close(t.wake) })
	return nil
}

func TestStopRawBridgeClosingUnblocksIdleRead(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	_, port, err := net.SplitHostPort(pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}

	dev := newIdleTun()
	if err := startRawBridge(dev, port); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for dev.reads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if dev.reads.Load() == 0 {
		t.Fatal("bridge did not enter TUN read")
	}

	done := make(chan struct{})
	go func() {
		stopRawBridgeClosing(func() { _ = dev.Close() })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("idle TUN read blocked bridge stop")
	}
}
