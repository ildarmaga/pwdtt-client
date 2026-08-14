package wb1

import (
	"container/heap"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	throughputPayloadBytes = 8 << 20
	throughputMinMbps      = 30.0
	oneWayDelay            = 36 * time.Millisecond
)

// delayedDir is one scheduler/heap goroutine delivering packets after a delay.
type delayedDir struct {
	mu     sync.Mutex
	h      delayHeap
	ready  [][]byte
	kick   chan struct{}
	wait   chan struct{}
	stop   chan struct{}
	dead   bool
	closed atomic.Bool
}

type delayPkt struct {
	at   time.Time
	data []byte
}

type delayHeap []delayPkt

func (h delayHeap) Len() int            { return len(h) }
func (h delayHeap) Less(i, j int) bool  { return h[i].at.Before(h[j].at) }
func (h delayHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *delayHeap) Push(x interface{}) { *h = append(*h, x.(delayPkt)) }
func (h *delayHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func newDelayedDir() *delayedDir {
	d := &delayedDir{
		kick: make(chan struct{}, 1),
		wait: make(chan struct{}, 1),
		stop: make(chan struct{}),
	}
	go d.run()
	return d
}

func (d *delayedDir) run() {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			stopTimer(timer)
		}
	}()
	for {
		d.mu.Lock()
		if d.dead && len(d.h) == 0 {
			d.mu.Unlock()
			d.signal(d.wait)
			return
		}
		if len(d.h) == 0 {
			d.mu.Unlock()
			select {
			case <-d.kick:
			case <-d.stop:
				d.mu.Lock()
				d.dead = true
				d.mu.Unlock()
			}
			continue
		}
		next := d.h[0]
		wait := time.Until(next.at)
		if wait > 0 {
			d.mu.Unlock()
			if timer == nil {
				timer = time.NewTimer(wait)
			} else {
				stopTimer(timer)
				timer.Reset(wait)
			}
			select {
			case <-timer.C:
			case <-d.kick:
			case <-d.stop:
				d.mu.Lock()
				d.dead = true
				d.mu.Unlock()
			}
			continue
		}
		pkt := heap.Pop(&d.h).(delayPkt)
		d.ready = append(d.ready, pkt.data)
		d.mu.Unlock()
		d.signal(d.wait)
	}
}

func (d *delayedDir) enqueue(at time.Time, data []byte) {
	d.mu.Lock()
	if d.dead {
		d.mu.Unlock()
		return
	}
	heap.Push(&d.h, delayPkt{at: at, data: data})
	head := d.h[0].at.Equal(at)
	d.mu.Unlock()
	if head {
		d.signal(d.kick)
	}
}

func (d *delayedDir) popReady() ([]byte, bool, bool) {
	d.mu.Lock()
	if len(d.ready) > 0 {
		p := d.ready[0]
		d.ready = d.ready[1:]
		d.mu.Unlock()
		return p, true, false
	}
	dead := d.dead
	d.mu.Unlock()
	return nil, false, dead
}

func (d *delayedDir) close() {
	if d.closed.CompareAndSwap(false, true) {
		close(d.stop)
	}
}

func (d *delayedDir) signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// delayedEndpoint is one side of a 36ms one-way delayed carrier.
// Loss applies only to first transmission of TypeData; retransmits (identical wire) pass.
type delayedEndpoint struct {
	peer    *delayedEndpoint
	ingress *delayedDir
	lossPct int
	reorder bool
	allPkt  bool
	mu      sync.Mutex
	seen    map[uint64]struct{}
	dataN   int
	pktN    int
	schedN  *atomic.Int32
}

func newDelayedPair(lossPct int, reorder bool) (*delayedEndpoint, *delayedEndpoint) {
	return newDelayedPairMode(lossPct, reorder, false)
}

func newDelayedPairAllPackets(lossPct int, reorder bool) (*delayedEndpoint, *delayedEndpoint) {
	return newDelayedPairMode(lossPct, reorder, true)
}

func newDelayedPairMode(lossPct int, reorder, allPkt bool) (*delayedEndpoint, *delayedEndpoint) {
	var sched atomic.Int32
	a := &delayedEndpoint{ingress: newDelayedDir(), lossPct: lossPct, reorder: reorder, allPkt: allPkt, seen: make(map[uint64]struct{}), schedN: &sched}
	b := &delayedEndpoint{ingress: newDelayedDir(), lossPct: lossPct, reorder: reorder, allPkt: allPkt, seen: make(map[uint64]struct{}), schedN: &sched}
	a.peer = b
	b.peer = a
	sched.Store(2)
	return a, b
}

func (c *delayedEndpoint) Send(_ context.Context, payload []byte) error {
	cp := append([]byte(nil), payload...)
	p := c.peer
	if p == nil || p.ingress.closed.Load() {
		return io.ErrClosedPipe
	}
	at := time.Now().Add(oneWayDelay)
	if c.allPkt {
		c.mu.Lock()
		c.pktN++
		n := c.pktN
		drop := c.lossPct > 0 && firstTxDrop(n, c.lossPct)
		reord := c.reorder && n%11 == 0
		c.mu.Unlock()
		if drop {
			return nil
		}
		if reord {
			at = at.Add(oneWayDelay / 4)
		}
		p.ingress.enqueue(at, cp)
		return nil
	}
	typ, _, _, ok := PeekRoute(cp)
	if ok && typ == TypeData {
		id := fnv64(cp)
		c.mu.Lock()
		_, seen := c.seen[id]
		if !seen {
			c.seen[id] = struct{}{}
			c.dataN++
			n := c.dataN
			drop := c.lossPct > 0 && firstTxDrop(n, c.lossPct)
			reord := c.reorder && n%11 == 0
			c.mu.Unlock()
			if drop {
				return nil
			}
			if reord {
				at = at.Add(oneWayDelay / 4)
			}
		} else {
			c.mu.Unlock()
		}
	}
	p.ingress.enqueue(at, cp)
	return nil
}

func (c *delayedEndpoint) Recv(ctx context.Context) ([]byte, error) {
	for {
		if p, ok, dead := c.ingress.popReady(); ok {
			return p, nil
		} else if dead {
			return nil, io.EOF
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.ingress.wait:
		case <-c.ingress.stop:
			if p, ok, _ := c.ingress.popReady(); ok {
				return p, nil
			}
			return nil, io.EOF
		}
	}
}

func (c *delayedEndpoint) Close() {
	c.ingress.close()
}

func firstTxDrop(n, lossPct int) bool {
	if lossPct <= 0 {
		return false
	}
	if lossPct >= 100 {
		return true
	}
	return (n-1)%100 < lossPct
}

func fnv64(p []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range p {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

func makeThroughputPayload() []byte {
	p := make([]byte, throughputPayloadBytes)
	for i := range p {
		p[i] = byte(i) ^ byte(i>>8) ^ byte(i>>16)
	}
	return p
}
