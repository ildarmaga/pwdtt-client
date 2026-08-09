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

func newTestDispatcher(t *testing.T, rawSticky bool) (*Dispatcher, net.PacketConn) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(ctx, pc, NewStats(), rawSticky, false, false)
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
	copyPkt := append([]byte(nil), pkt...)
	if flowHash(pkt) != flowHash(copyPkt) {
		t.Fatal("unstable")
	}
}

func TestFlowHashBidirectionalSame(t *testing.T) {
	up := tcpPkt(2, 50000, 443)
	down := make([]byte, len(up))
	copy(down, up)
	copy(down[12:16], up[16:20])
	copy(down[16:20], up[12:16])
	binary.BigEndian.PutUint16(down[20:22], 443)
	binary.BigEndian.PutUint16(down[22:24], 50000)
	if flowHash(up) != flowHash(down) {
		t.Fatalf("up/down 5-tuple must hash equal: %x vs %x", flowHash(up), flowHash(down))
	}
}

func TestRawStickyKeepsOneFlowOnWorker(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		stats:     NewStats(),
		ctx:       context.Background(),
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
	}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 64)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 64)}
	d.workers = []*WorkerSlot{w1, w2}

	pkt := tcpPkt(1, 1234, 443)
	for i := 0; i < 32; i++ {
		d.dispatchSticky(append([]byte(nil), pkt...))
	}
	if len(w1.SendCh)+len(w2.SendCh) != 32 {
		t.Fatalf("want 32 delivered, got w1=%d w2=%d", len(w1.SendCh), len(w2.SendCh))
	}
	if len(w1.SendCh) > 0 && len(w2.SendCh) > 0 {
		t.Fatal("one TCP flow must stay on a single worker (no chunk multipath)")
	}
}

func TestRawStickySpreadsDifferentFlows(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		stats:     NewStats(),
		ctx:       context.Background(),
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
	}
	for id := 1; id <= 8; id++ {
		d.workers = append(d.workers, &WorkerSlot{ID: id, SendCh: make(chan []byte, 64)})
	}
	for sport := uint16(1000); sport < 1064; sport++ {
		d.dispatchSticky(tcpPkt(1, sport, 443))
	}
	hit := 0
	for _, w := range d.workers {
		if len(w.SendCh) > 0 {
			hit++
		}
	}
	if hit < 2 {
		t.Fatalf("different flows should spread, workers hit=%d", hit)
	}
}

func TestRawStickySpreadsFlowsWhenQueuesDrain(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		stats:     NewStats(),
		ctx:       context.Background(),
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
	}
	for id := 1; id <= 4; id++ {
		d.workers = append(d.workers, &WorkerSlot{ID: id, SendCh: make(chan []byte, 1)})
	}
	hit := make(map[int]bool)
	for sport := uint16(2000); sport < 2016; sport++ {
		pkt := tcpPkt(1, sport, 443)
		d.dispatchSticky(pkt)
		for _, w := range d.workers {
			select {
			case queued := <-w.SendCh:
				putPktBuf(queued)
				hit[w.ID] = true
			default:
			}
		}
	}
	if len(hit) != len(d.workers) {
		t.Fatalf("drained queues must not pin every new flow to one worker: hit=%v", hit)
	}
}

func TestRawStickyRegisterCreatesSendCh(t *testing.T) {
	d, _ := newTestDispatcher(t, true)
	if !d.rawSticky {
		t.Fatal("sticky expected by default")
	}
	w := &WorkerSlot{ID: 7}
	d.Register(w)
	if w.SendCh == nil {
		t.Fatal("SendCh not created")
	}
}

func TestRawStickyRebindAfterUnregister(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		stats:     NewStats(),
		ctx:       context.Background(),
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
	}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 8)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 8)}
	d.workers = []*WorkerSlot{w1, w2}
	pkt := tcpPkt(1, 9999, 443)
	d.dispatchSticky(append([]byte(nil), pkt...))
	var first *WorkerSlot
	if len(w1.SendCh) == 1 {
		first = w1
	} else {
		first = w2
	}
	d.Unregister(first)
	d.dispatchSticky(append([]byte(nil), pkt...))
	other := w1
	if first == w1 {
		other = w2
	}
	if len(other.SendCh) != 1 {
		t.Fatalf("after unregister want rebind to remaining worker, got %d", len(other.SendCh))
	}
}

func TestRawStickyDoesNotMigrateWhenAffineQueueIsFull(t *testing.T) {
	d := &Dispatcher{
		rawSticky: true,
		stats:     NewStats(),
		ctx:       context.Background(),
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
	}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 1)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 1)}
	d.workers = []*WorkerSlot{w1, w2}
	pkt := tcpPkt(1, 10001, 443)
	key := flowKey(pkt)
	d.flowAff[key] = w1.ID
	d.flowExp[key] = time.Now().Add(time.Minute).UnixNano()
	w1.SendCh <- tcpPkt(1, 9999, 443)

	d.dispatchSticky(pkt)
	if len(w2.SendCh) != 0 || d.flowAff[key] != w1.ID {
		t.Fatalf("established flow migrated: w2=%d affinity=%d", len(w2.SendCh), d.flowAff[key])
	}
	if d.stats.DroppedUp != 1 {
		t.Fatalf("want one explicit drop, got %d", d.stats.DroppedUp)
	}
}
