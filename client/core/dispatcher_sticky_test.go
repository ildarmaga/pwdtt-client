package core

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
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

func newTestDispatcher(t *testing.T, rawSticky bool) (*Dispatcher, net.PacketConn) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(ctx, pc, NewStats(), rawSticky)
	t.Cleanup(func() {
		cancel()
		_ = pc.Close() // разблокирует readLoop до Wait
		done := make(chan struct{})
		go func() {
			d.Shutdown()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Shutdown hung (readLoop not unblocked)")
		}
	})
	return d, pc
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
	// Порт тоже в хэше.
	pkt2 := tcpPkt(3, 1235, 443)
	if flowHash(pkt2) == flowHash(tcpPkt(3, 1234, 443)) {
		t.Fatal("sport change should change hash")
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

	for i := 0; i < 200; i++ {
		got := d.pickStickyLocked(pkt)
		if got == nil || got.ID != 1 {
			t.Fatalf("iter %d: remapped to %#v (hash%%n bug)", i, got)
		}
	}
}

func TestStickyAffinitySurvivesGrowFrom1To18(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
		stats:     NewStats(),
	}
	workers := make([]*WorkerSlot, 18)
	for id := 1; id <= 18; id++ {
		workers[id-1] = &WorkerSlot{ID: id, SendCh: make(chan []byte, 4)}
	}

	// Закрепляем потоки при 1 воркере, потом растим пул до 18.
	d.workers = workers[:1]
	want := map[uint16]int{}
	for sport := uint16(10000); sport < 10080; sport++ {
		w := d.pickStickyLocked(tcpPkt(24, sport, 443))
		if w == nil {
			t.Fatal("nil pick")
		}
		want[sport] = w.ID
	}
	for n := 2; n <= 18; n++ {
		d.workers = workers[:n]
		for sport, id := range want {
			got := d.pickStickyLocked(tcpPkt(24, sport, 443))
			if got == nil || got.ID != id {
				t.Fatalf("sport=%d at N=%d: want worker %d got %#v", sport, n, id, got)
			}
		}
	}
}

func TestStickyAffinityRebindsAfterWorkerDeath(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
		stats:     NewStats(),
		ctx:       context.Background(),
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
	d.Unregister(dead)

	got := d.pickStickyLocked(pkt)
	if got == nil || got.ID != alive.ID {
		t.Fatalf("want rebind to %d, got %#v", alive.ID, got)
	}
}

func TestStickyDispatchDelivers(t *testing.T) {
	d, _ := newTestDispatcher(t, true)

	w := &WorkerSlot{ID: 7}
	d.Register(w)
	if w.SendCh == nil {
		t.Fatal("SendCh not created")
	}
	if cap(w.SendCh) != rawWorkerSendBuf {
		t.Fatalf("SendCh cap=%d want %d", cap(w.SendCh), rawWorkerSendBuf)
	}

	pkt := tcpPkt(24, 7000, 443)
	d.dispatchSticky(append([]byte(nil), pkt...))

	select {
	case got := <-w.SendCh:
		if len(got) != len(pkt) {
			t.Fatalf("len %d", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting packet")
	}
}

func TestStickyDispatchBlocksInsteadOfDrop(t *testing.T) {
	d, _ := newTestDispatcher(t, true)
	// Крошечный буфер — имитируем заполненную очередь.
	w := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 1)}
	d.Register(w)
	w.SendCh <- []byte{1} // забили 1 слот

	var delivered atomic.Bool
	go func() {
		d.dispatchSticky(tcpPkt(24, 7100, 443))
		delivered.Store(true)
	}()

	time.Sleep(50 * time.Millisecond)
	if delivered.Load() {
		t.Fatal("dispatch returned while channel full — should block, not drop")
	}

	<-w.SendCh // освободили слот
	deadline := time.After(time.Second)
	for !delivered.Load() {
		select {
		case <-deadline:
			t.Fatal("blocked forever after space freed")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	select {
	case <-w.SendCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("packet never delivered after unblock")
	}
}

func TestStickyDifferentFlowsCanSplit(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
		stats:     NewStats(),
	}
	for id := 1; id <= 9; id++ {
		d.workers = append(d.workers, &WorkerSlot{ID: id, SendCh: make(chan []byte, 1)})
	}
	seen := map[int]bool{}
	for sport := uint16(20000); sport < 20100; sport++ {
		w := d.pickStickyLocked(tcpPkt(24, sport, 443))
		if w != nil {
			seen[w.ID] = true
		}
	}
	if len(seen) < 2 {
		t.Fatalf("expected flows to spread across workers, got %#v", seen)
	}
}

func TestStickyConcurrentPickStable(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
		stats:     NewStats(),
		mu:        sync.Mutex{},
	}
	for id := 1; id <= 12; id++ {
		d.workers = append(d.workers, &WorkerSlot{ID: id, SendCh: make(chan []byte, 8)})
	}
	pkt := tcpPkt(24, 8000, 443)

	d.mu.Lock()
	want := d.pickStickyLocked(pkt).ID
	d.mu.Unlock()

	var wg sync.WaitGroup
	errs := make(chan int, 64)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				d.mu.Lock()
				got := d.pickStickyLocked(pkt)
				d.mu.Unlock()
				if got == nil || got.ID != want {
					id := -1
					if got != nil {
						id = got.ID
					}
					errs <- id
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for id := range errs {
		t.Fatalf("concurrent remap: want %d got %d", want, id)
	}
}

func TestHashModNRemapsOnJoin(t *testing.T) {
	// Старый баг: при росте N один и тот же flowHash даёт другой индекс.
	remapped := 0
	for sport := uint16(4000); sport < 4200; sport++ {
		hh := flowHash(tcpPkt(24, sport, 443))
		if int(hh%1) != int(hh%18) {
			remapped++
		}
	}
	if remapped == 0 {
		t.Fatal("expected hash%N to remap for some flows when N grows 1→18")
	}
}
