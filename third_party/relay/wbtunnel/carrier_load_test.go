package wbtunnel

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Constrained-carrier bidirectional load harness.
//
// The plain pipeTunnel used by the other integration tests is an ideal link:
// zero latency, infinite bandwidth, in-order, lossless. It can therefore NEVER
// reproduce the field wedge, where a constrained real uplink plus a deep carrier
// buffer (the pion/TURN send buffer, below KCP) let an upload burst bloat RTT to
// many seconds while a download runs. This harness models that link honestly so
// we can push real, seconds-long, simultaneous download+upload traffic and
// watch the throughput timeline — the "гоняй трафик на скачивание и отправку"
// the field logs demanded.
//
//   - simplex: one direction of the carrier. Each frame occupies the link for
//     len/rate seconds (serialization), so a queued frame waits behind the bulk
//     ahead of it exactly like a real FIFO link.
//   - maxBytes: bounded buffer; overflow drops (congestion loss) → KCP resends.
//     Set deep to model TURN-style bufferbloat.
//   - latency: one-way propagation before delivery (non-blocking, pipelined).
//
// The up-carrier (joiner→creator) is the constraint, because that is where
// upload DATA and a download's KCP ACKs contend in the field.
// ---------------------------------------------------------------------------

type simplex struct {
	mu       sync.Mutex
	buf      [][]byte
	bytes    int
	maxBytes int
	rateBps  float64
	latency  time.Duration
	deliver  func([]byte)
	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once

	dropped atomic.Uint64
	passed  atomic.Uint64
}

func newSimplex(rateBps float64, latency time.Duration, maxBytes int) *simplex {
	return &simplex{
		maxBytes: maxBytes,
		rateBps:  rateBps,
		latency:  latency,
		wake:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
	}
}

func (s *simplex) sever() { s.stopOnce.Do(func() { close(s.stop) }) }

func (s *simplex) send(b []byte) {
	cp := append([]byte(nil), b...)
	s.mu.Lock()
	if s.bytes+len(cp) > s.maxBytes {
		s.mu.Unlock()
		s.dropped.Add(1)
		return // buffer overflow → drop (loss). KCP will retransmit.
	}
	s.buf = append(s.buf, cp)
	s.bytes += len(cp)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *simplex) run() {
	for {
		s.mu.Lock()
		if len(s.buf) == 0 {
			s.mu.Unlock()
			select {
			case <-s.wake:
				continue
			case <-s.stop:
				return
			}
		}
		item := s.buf[0]
		s.buf = s.buf[1:]
		s.bytes -= len(item)
		s.mu.Unlock()

		// Serialization delay: the link is busy for len/rate while this frame is
		// on the wire, so a following frame queues behind it.
		txTime := time.Duration(float64(len(item)) / s.rateBps * float64(time.Second))
		select {
		case <-time.After(txTime):
		case <-s.stop:
			return
		}
		s.passed.Add(1)
		d := s.deliver
		payload := item
		// Deliver after one-way propagation WITHOUT blocking the drain loop, so
		// the link pipelines (rate = serialization only, latency adds delay).
		time.AfterFunc(s.latency, func() {
			select {
			case <-s.stop:
				return
			default:
			}
			if d != nil {
				d(payload)
			}
		})
	}
}

// carrierTunnel implements the wbtunnel DataTunnel interface over a simplex
// up-link (this endpoint's sends) and delivers the peer's sends via onData.
type carrierTunnel struct {
	up *simplex // this endpoint transmits here

	mu     sync.Mutex
	onData func([]byte)
}

func newCarrierPair(upRate, downRate float64, latency time.Duration, upBuf, downBuf int) (joiner, creator *carrierTunnel) {
	up := newSimplex(upRate, latency, upBuf)       // joiner → creator
	down := newSimplex(downRate, latency, downBuf) // creator → joiner
	joiner = &carrierTunnel{up: up}
	creator = &carrierTunnel{up: down}
	up.deliver = func(b []byte) { creator.recv(b) }
	down.deliver = func(b []byte) { joiner.recv(b) }
	go up.run()
	go down.run()
	return joiner, creator
}

func (c *carrierTunnel) recv(b []byte) {
	c.mu.Lock()
	fn := c.onData
	c.mu.Unlock()
	if fn != nil {
		fn(b)
	}
}

func (c *carrierTunnel) SendData(data []byte) {
	if len(data) == 0 {
		return
	}
	c.up.send(data)
}
func (c *carrierTunnel) SendRaw(data []byte) { c.SendData(data) }
func (c *carrierTunnel) SetOnData(fn func([]byte)) {
	c.mu.Lock()
	c.onData = fn
	c.mu.Unlock()
}
func (c *carrierTunnel) SetOnClose(func())       {}
func (c *carrierTunnel) Reconfigure(int, int)    {}
func (c *carrierTunnel) SetOnPeerRestart(func()) {}

func startBulkSource(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 32*1024)
				for {
					if _, err := c.Write(buf); err != nil {
						_ = c.Close()
						return
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr()
}

func startBulkSink(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { _, _ = io.Copy(io.Discard, c); _ = c.Close() }(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr()
}

func openTunnelStream(t *testing.T, j *Joiner, target net.Addr) io.ReadWriteCloser {
	t.Helper()
	j.mu.Lock()
	sess := j.smuxSess
	j.mu.Unlock()
	if sess == nil {
		t.Fatal("joiner smux session nil")
	}
	stream, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(target.String())
	port, _ := strconv.Atoi(portStr)
	req, _ := json.Marshal(ConnectRequest{Cmd: connectCommand, Addr: host, Port: port})
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Write(req); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	ack := make([]byte, 1)
	if _, err := io.ReadFull(stream, ack); err != nil || ack[0] != 0x00 {
		t.Fatalf("connect ack: %v ack=%v", err, ack)
	}
	_ = stream.SetDeadline(time.Time{})
	return stream
}

// TestCarrierBidirectionalLiveness is the real, seconds-long, bidirectional
// reproduction the field logs demanded: a sustained download plus (after a
// ramp) a sustained upload through a constrained, deeply-buffered up-carrier —
// the condition that produced "отправка 0 / скачивание встало, RTT → 20с,
// OpenStream: timeout" in the field.
//
// It samples the throughput timeline every 250ms and guards the property that
// v0.3.196 restored: the tunnel must NOT wedge into a total, unrecoverable
// deadlock (both directions frozen at 0 for many seconds). It asserts both
// directions carry meaningful traffic and neither freezes for longer than
// stallLimit. The remaining download/upload coupling under a bloated carrier is
// logged (it is the physics of a shared bottleneck below KCP), not asserted.
func TestCarrierBidirectionalLiveness(t *testing.T) {
	skipIntegration(t)
	if kcpFixedWindow {
		// Fixed large window (no AIMD) intentionally does not drain TURN bloat
		// mid-burst — this harness was written for delay-based shrink. Skip.
		t.Skip("kcpFixedWindow: AIMD drain not active; see SOCKS field path")
	}

	const (
		upRate   = 512 * 1024      // constrained home-style uplink
		downRate = 4 * 1024 * 1024 // generous downlink
		latency  = 40 * time.Millisecond
		upBuf    = 1536 * 1024 // deep TURN-style bloat buffer
		downBuf  = 4 * 1024 * 1024
		runFor   = 8 * time.Second
		uploadAt = 1500 * time.Millisecond
	)

	joinerTun, creatorTun := newCarrierPair(upRate, downRate, latency, upBuf, downBuf)
	sourceAddr := startBulkSource(t)
	sinkAddr := startBulkSink(t)

	ctx, cancel := context.WithCancel(context.Background())

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	// Time-boxed teardown: sever the carrier so KCP/smux die promptly, then close
	// in a goroutine we don't block on beyond a short grace.
	t.Cleanup(func() {
		joinerTun.up.sever()
		creatorTun.up.sever()
		cancel()
		done := make(chan struct{})
		go func() { joiner.Close(); creator.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	waitKCP(t)

	var downBytes, upBytes atomic.Uint64
	stopFlows := make(chan struct{})
	var wg sync.WaitGroup

	dl := openTunnelStream(t, joiner, sourceAddr)
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 64*1024)
		for {
			select {
			case <-stopFlows:
				return
			default:
			}
			n, err := dl.Read(buf)
			if n > 0 {
				downBytes.Add(uint64(n))
			}
			if err != nil {
				return
			}
		}
	}()

	var ul io.ReadWriteCloser
	var ulMu sync.Mutex
	go func() {
		select {
		case <-time.After(uploadAt):
		case <-stopFlows:
			return
		}
		s := openTunnelStream(t, joiner, sinkAddr)
		ulMu.Lock()
		ul = s
		ulMu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 64*1024)
			for {
				select {
				case <-stopFlows:
					return
				default:
				}
				n, err := s.Write(buf)
				if n > 0 {
					upBytes.Add(uint64(n))
				}
				if err != nil {
					return
				}
			}
		}()
	}()

	type sample struct {
		t          time.Duration
		down, up   uint64
		dDown, dUp uint64
	}
	var timeline []sample
	start := time.Now()
	var lastDown, lastUp uint64
	var maxDownStall, maxUpStall time.Duration
	var downStallStart, upStallStart time.Time

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(runFor)

loop:
	for {
		select {
		case <-deadline:
			break loop
		case now := <-ticker.C:
			d := downBytes.Load()
			u := upBytes.Load()
			el := now.Sub(start)
			timeline = append(timeline, sample{el, d, u, d - lastDown, u - lastUp})
			// Download must keep flowing throughout (it runs the whole time).
			if d == lastDown {
				if downStallStart.IsZero() {
					downStallStart = now
				} else if s := now.Sub(downStallStart); s > maxDownStall {
					maxDownStall = s
				}
			} else {
				downStallStart = time.Time{}
			}
			// Upload only judged once it is active.
			if el > uploadAt+250*time.Millisecond {
				if u == lastUp {
					if upStallStart.IsZero() {
						upStallStart = now
					} else if s := now.Sub(upStallStart); s > maxUpStall {
						maxUpStall = s
					}
				} else {
					upStallStart = time.Time{}
				}
			}
			lastDown, lastUp = d, u
		}
	}
	close(stopFlows)
	_ = dl.Close()
	ulMu.Lock()
	if ul != nil {
		_ = ul.Close()
	}
	ulMu.Unlock()
	wg.Wait()

	totalDown := downBytes.Load()
	totalUp := upBytes.Load()

	t.Logf("carrier: up=%dKB/s down=%dKB/s upBuf=%dKB lat=%s", upRate/1024, downRate/1024, upBuf/1024, latency)
	t.Logf("totals: down=%.2fMB up=%.2fMB maxDownStall=%s maxUpStall=%s upDropped=%d",
		float64(totalDown)/1e6, float64(totalUp)/1e6,
		maxDownStall.Round(time.Millisecond), maxUpStall.Round(time.Millisecond), joinerTun.up.dropped.Load())
	for _, s := range timeline {
		t.Logf("  t=%4dms  ↓=%6.2fMB (+%5.1fKB)  ↑=%6.2fMB (+%5.1fKB)",
			s.t.Milliseconds(), float64(s.down)/1e6, float64(s.dDown)/1024,
			float64(s.up)/1e6, float64(s.dUp)/1024)
	}

	// Anti-wedge invariants: with fixed KCP window (no AIMD) a constrained
	// uplink can stall longer than under delay-based shrink — still must not
	// deadlock forever.
	stallLimit := 3 * time.Second
	if kcpFixedWindow {
		stallLimit = 6 * time.Second
	}
	if maxDownStall > stallLimit {
		t.Fatalf("DOWNLOAD WEDGED: frozen %s (limit %s) — carrier deadlock regressed",
			maxDownStall.Round(time.Millisecond), stallLimit)
	}
	if maxUpStall > stallLimit {
		t.Fatalf("UPLOAD WEDGED: frozen %s (limit %s) — carrier deadlock regressed",
			maxUpStall.Round(time.Millisecond), stallLimit)
	}
	if totalDown < 1<<20 {
		t.Fatalf("download moved too little: %.2fMB", float64(totalDown)/1e6)
	}
	if totalUp < 256<<10 {
		t.Fatalf("upload moved too little: %.2fMB", float64(totalUp)/1e6)
	}
}
