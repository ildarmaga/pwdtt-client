package core

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func tcpPkt(srcHost byte, sport, dport uint16) []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], 40)
	pkt[8] = 64
	pkt[9] = 6
	pkt[12], pkt[13], pkt[14], pkt[15] = 10, 70, 0, srcHost
	pkt[16], pkt[17], pkt[18], pkt[19] = 1, 2, 3, 4
	binary.BigEndian.PutUint16(pkt[20:22], sport)
	binary.BigEndian.PutUint16(pkt[22:24], dport)
	return pkt
}

func newTestDispatcher(t *testing.T, rawMulti bool) (*Dispatcher, net.PacketConn) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(ctx, pc, NewStats(), rawMulti)
	t.Cleanup(func() {
		cancel()
		_ = pc.Close()
		done := make(chan struct{})
		go func() {
			d.Shutdown()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Shutdown hung")
		}
	})
	return d, pc
}

func TestFlowHashStable(t *testing.T) {
	pkt := tcpPkt(3, 1234, 443)
	if flowHash(pkt) != flowHash(pkt) {
		t.Fatal("unstable")
	}
}

func TestRawChunkStaysOnWorkerThenRotates(t *testing.T) {
	d := &Dispatcher{
		rawMulti: true,
		stats:    NewStats(),
		ctx:      context.Background(),
	}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 64)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 64)}
	d.workers = []*WorkerSlot{w1, w2}

	for i := 0; i < chunkSize; i++ {
		d.dispatchChunk(tcpPkt(1, uint16(1000+i), 443))
	}
	if got := len(w1.SendCh); got != chunkSize {
		t.Fatalf("want %d on w1, got %d", chunkSize, got)
	}
	if len(w2.SendCh) != 0 {
		t.Fatal("w2 should be empty during first chunk")
	}
	d.dispatchChunk(tcpPkt(1, 2000, 443))
	if len(w2.SendCh) != 1 {
		t.Fatalf("after chunk rotate want 1 on w2, got %d", len(w2.SendCh))
	}
}

func TestRawChunkStealsWhenFull(t *testing.T) {
	d := &Dispatcher{
		rawMulti: true,
		stats:    NewStats(),
		ctx:      context.Background(),
	}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 1)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 4)}
	d.workers = []*WorkerSlot{w1, w2}
	w1.SendCh <- []byte{1} // full

	d.dispatchChunk(tcpPkt(1, 3000, 443))
	if len(w2.SendCh) != 1 {
		t.Fatalf("want steal to w2, got w2=%d w1=%d", len(w2.SendCh), len(w1.SendCh))
	}
}

func TestRawDispatchDelivers(t *testing.T) {
	d, _ := newTestDispatcher(t, true)
	w := &WorkerSlot{ID: 7}
	d.Register(w)
	if w.SendCh == nil {
		t.Fatal("SendCh not created")
	}
	pkt := tcpPkt(24, 7000, 443)
	d.dispatchChunk(append([]byte(nil), pkt...))
	select {
	case got := <-w.SendCh:
		if len(got) != len(pkt) {
			t.Fatalf("len %d", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestRawChunkSpreadsAcrossWorkers(t *testing.T) {
	d := &Dispatcher{
		rawMulti: true,
		stats:    NewStats(),
		ctx:      context.Background(),
	}
	for id := 1; id <= 4; id++ {
		d.workers = append(d.workers, &WorkerSlot{ID: id, SendCh: make(chan []byte, 256)})
	}
	for i := 0; i < 64; i++ {
		d.dispatchChunk(tcpPkt(1, uint16(4000+i), 443))
	}
	hit := 0
	for _, w := range d.workers {
		if len(w.SendCh) > 0 {
			hit++
		}
	}
	if hit < 2 {
		t.Fatalf("expected multipath spread, workers with pkts=%d", hit)
	}
}
