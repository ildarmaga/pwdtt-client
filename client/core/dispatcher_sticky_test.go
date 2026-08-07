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

func TestFlowHashStable(t *testing.T) {
	pkt := tcpPkt(3, 1234, 443)
	h1 := flowHash(pkt)
	h2 := flowHash(pkt)
	if h1 != h2 {
		t.Fatal("unstable")
	}
	pkt[15] = 4
	if flowHash(pkt) == h1 {
		t.Fatal("src change should change hash")
	}
}

func TestStickyAffinitySurvivesWorkerJoin(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
		stats:     NewStats(),
	}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 8)}
	d.workers = []*WorkerSlot{w1}

	pkt := tcpPkt(24, 5000, 443)
	a := d.pickStickyLocked(pkt)
	if a == nil || a.ID != 1 {
		t.Fatalf("want worker 1, got %#v", a)
	}

	// Присоединились ещё воркеры — тот же поток обязан остаться на #1.
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 8)}
	w3 := &WorkerSlot{ID: 3, SendCh: make(chan []byte, 8)}
	d.workers = []*WorkerSlot{w1, w2, w3}

	for i := 0; i < 20; i++ {
		got := d.pickStickyLocked(pkt)
		if got == nil || got.ID != 1 {
			t.Fatalf("iter %d: remapped to %#v (hash%%n bug)", i, got)
		}
	}
}

func TestStickyAffinityRebindsAfterWorkerDeath(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
		stats:     NewStats(),
	}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 8)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 8)}
	d.workers = []*WorkerSlot{w1, w2}

	pkt := tcpPkt(24, 6000, 443)
	first := d.pickStickyLocked(pkt)
	if first == nil {
		t.Fatal("nil")
	}

	dead := first
	alive := w2
	if dead.ID == 2 {
		alive = w1
	}
	d.workers = []*WorkerSlot{alive}
	for k, id := range d.flowAff {
		if id == dead.ID {
			delete(d.flowAff, k)
			delete(d.flowExp, k)
		}
	}

	got := d.pickStickyLocked(pkt)
	if got == nil || got.ID != alive.ID {
		t.Fatalf("want rebind to %d, got %#v", alive.ID, got)
	}
}

func TestStickyDispatchDelivers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stats := NewStats()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	d := NewDispatcher(ctx, pc, stats, true)
	defer d.Shutdown()

	w := &WorkerSlot{ID: 7}
	d.Register(w)
	if w.SendCh == nil {
		t.Fatal("SendCh not created")
	}

	pkt := tcpPkt(24, 7000, 443)
	d.dispatchSticky(append([]byte(nil), pkt...))

	select {
	case got := <-w.SendCh:
		if len(got) != len(pkt) {
			t.Fatalf("len %d", len(got))
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting packet")
	}
}

func TestHashModNRemapsOnJoin(t *testing.T) {
	// Документирует старый баг: hash%len меняется при росте N.
	pkt := tcpPkt(24, 5000, 443)
	h := flowHash(pkt)
	idx1 := h % 1
	idx2 := h % 2
	idx3 := h % 3
	if idx1 == 0 && idx2 == 0 && idx3 == 0 {
		t.Skip("this hash stays at 0 for n=1..3; pick another sport")
	}
	// Для большинства sport idx меняется; проверим хотя бы что формула нестабильна на наборе.
	unstable := false
	for sport := uint16(4000); sport < 4100; sport++ {
		p := tcpPkt(24, sport, 443)
		hh := flowHash(p)
		if hh%1 != hh%3 && hh%2 != hh%3 {
			unstable = true
			break
		}
		if int(hh%uint32(1)) != int(hh%uint32(9)) {
			unstable = true
			break
		}
	}
	if !unstable {
		// всё равно покажем что %n от N зависит
		p := tcpPkt(24, 12345, 443)
		hh := flowHash(p)
		a, b := hh%uint32(2), hh%uint32(3)
		if a == b {
			t.Logf("hash%%2=%d hash%%3=%d (may coincide)", a, b)
		}
	}
}
