package wb1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type shakyCarrier struct {
	peer    *shakyCarrier
	mu      sync.Mutex
	q       [][]byte
	wait    chan struct{}
	dead    bool
	sent    int
	dropMod int
	swapMod int
}

func newShakyPair(dropMod, swapMod int) (*shakyCarrier, *shakyCarrier) {
	a := &shakyCarrier{wait: make(chan struct{}, 1), dropMod: dropMod, swapMod: swapMod}
	b := &shakyCarrier{wait: make(chan struct{}, 1), dropMod: dropMod, swapMod: swapMod}
	a.peer = b
	b.peer = a
	return a, b
}

func (c *shakyCarrier) Send(_ context.Context, payload []byte) error {
	cp := append([]byte(nil), payload...)
	p := c.peer
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return io.ErrClosedPipe
	}
	p.sent++
	if p.dropMod > 0 && p.sent%p.dropMod == 0 {
		return nil
	}
	if p.swapMod > 0 && len(p.q) > 0 && p.sent%p.swapMod == 0 {
		last := p.q[len(p.q)-1]
		p.q[len(p.q)-1] = cp
		p.q = append(p.q, last)
	} else {
		p.q = append(p.q, cp)
	}
	select {
	case p.wait <- struct{}{}:
	default:
	}
	return nil
}

func (c *shakyCarrier) Recv(ctx context.Context) ([]byte, error) {
	for {
		c.mu.Lock()
		if len(c.q) > 0 {
			p := c.q[0]
			c.q = c.q[1:]
			c.mu.Unlock()
			return p, nil
		}
		if c.dead {
			c.mu.Unlock()
			return nil, io.EOF
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.wait:
		}
	}
}

func (c *shakyCarrier) Close() {
	c.mu.Lock()
	c.dead = true
	select {
	case c.wait <- struct{}{}:
	default:
	}
	c.mu.Unlock()
}

func startMuxPair(t *testing.T, left, right Carrier) (*Mux, *Mux, context.CancelFunc) {
	t.Helper()
	key := testKey(t)
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	return joiner, creator, cancel
}

func TestARQOrderedDeliveryUnderReorder(t *testing.T) {
	left, right := newShakyPair(0, 2)
	joiner, creator, cancel := startMuxPair(t, left, right)
	defer cancel()
	defer left.Close()
	defer right.Close()

	ctx, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel2()

	got := make(chan []byte, 1)
	go func() {
		_, conn, err := creator.Accept(ctx)
		if err != nil {
			got <- nil
			return
		}
		buf := make([]byte, 12)
		if _, err := io.ReadFull(conn, buf); err != nil {
			got <- nil
			return
		}
		got <- buf
	}()

	conn, err := joiner.Dial(ctx, "ord.example:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []string{"aaa", "bbb", "ccc", "ddd"} {
		if _, err := conn.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case buf := <-got:
		if string(buf) != "aaabbbcccddd" {
			t.Fatalf("reordered delivery %q", buf)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

func TestARQDuplicateSuppression(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()

	got := make(chan []byte, 1)
	go func() {
		_, conn, err := creator.Accept(ctx)
		if err != nil {
			return
		}
		buf := make([]byte, 4)
		n, _ := io.ReadFull(conn, buf)
		got <- buf[:n]
	}()

	conn, err := joiner.Dial(ctx, "dup.example:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	sc := conn.(*streamConn)
	dup, err := Pack(key, Frame{
		Type: TypeData, StreamID: sc.id, Dest: testSID(2), Src: testSID(1),
		Epoch: joiner.epoch, Seq: sc.lastDataSeq(), Payload: []byte("ping"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := left.Send(ctx, dup); err != nil {
		t.Fatal(err)
	}
	select {
	case buf := <-got:
		if string(buf) != "ping" {
			t.Fatalf("duplicated bytes %q", buf)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	left.Close()
	right.Close()
	cancel()
}

func TestARQRecoversDeterministicLoss(t *testing.T) {
	// drop every 12th (~8%) and swap every 5th with previous.
	left, right := newShakyPair(12, 5)
	joiner, creator, cancel := startMuxPair(t, left, right)
	defer cancel()
	defer left.Close()
	defer right.Close()

	const n = 64 * 1024
	payload := make([]byte, n)
	for i := range payload {
		payload[i] = byte(i)
	}
	sum := sha256.Sum256(payload)

	ctx, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()

	errCh := make(chan error, 1)
	go func() {
		_, conn, err := creator.Accept(ctx)
		if err != nil {
			errCh <- err
			return
		}
		got := make([]byte, n)
		if _, err := io.ReadFull(conn, got); err != nil {
			errCh <- err
			return
		}
		if sha256.Sum256(got) != sum {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		errCh <- nil
	}()

	start := time.Now()
	conn, err := joiner.Dial(ctx, "loss.example:443")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("64KB lossy transfer timed out")
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("lossy 64KB took %s, want bounded well under stall timeout", d)
	}
}

func TestARQWindowBackpressureUnblocksOnAck(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	dropAck := atomic.Bool{}
	creatorSend := &filterCarrier{inner: right, drop: func(p []byte) bool {
		if !dropAck.Load() {
			return false
		}
		typ, _, _, ok := PeekRoute(p)
		return ok && typ == TypeAck
	}}
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, creatorSend)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()

	go func() {
		_, conn, err := creator.Accept(ctx)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, conn)
	}()

	conn, err := joiner.Dial(ctx, "win.example:1")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	dropAck.Store(true)
	chunk := bytes.Repeat([]byte("w"), MaxPayload)
	for i := 0; i < ARQWindow; i++ {
		if _, err := conn.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	blocked := make(chan error, 1)
	go func() {
		_, err := conn.Write(chunk)
		blocked <- err
	}()
	select {
	case err := <-blocked:
		t.Fatalf("write should block on full window, got err=%v", err)
	case <-time.After(200 * time.Millisecond):
	}
	dropAck.Store(false)
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write did not unblock after ACKs resumed")
	}
	cancel()
	left.Close()
	right.Close()
}

func TestReliableOpenDataFinTLSSized(t *testing.T) {
	left, right := newCarrierPair()
	joiner, creator, cancel := startMuxPair(t, left, right)
	defer cancel()
	defer left.Close()
	defer right.Close()

	const n = 16 * 1024
	payload := bytes.Repeat([]byte("T"), n)
	ctx, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel2()

	done := make(chan []byte, 1)
	go func() {
		_, conn, err := creator.Accept(ctx)
		if err != nil {
			done <- nil
			return
		}
		got := make([]byte, n)
		if _, err := io.ReadFull(conn, got); err != nil {
			done <- nil
			return
		}
		_ = conn.Close()
		done <- got
	}()

	conn, err := joiner.Dial(ctx, "tls.example:443")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case got := <-done:
		if !bytes.Equal(got, payload) {
			t.Fatalf("tls copy len %d", len(got))
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

func TestTwoJoinersLossOnADoesNotStallB(t *testing.T) {
	key := testKey(t)
	creatorSID, aSID, bSID := testSID(1), testSID(2), testSID(3)
	bus := newBroadcastBus()

	lossyWrap := func(e *busEndpoint, dropMod int) Carrier {
		return &lossyBus{inner: e, dropMod: dropMod}
	}

	creatorA := NewMux(key, bus.endpoint(creatorSID, aSID))
	creatorA.SetRoute(creatorSID, aSID)
	creatorB := NewMux(key, bus.endpoint(creatorSID, bSID))
	creatorB.SetRoute(creatorSID, bSID)
	joinerA := NewMux(key, lossyWrap(bus.endpoint(aSID, creatorSID), 8))
	joinerA.SetRoute(aSID, creatorSID)
	joinerB := NewMux(key, bus.endpoint(bSID, creatorSID))
	joinerB.SetRoute(bSID, creatorSID)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = creatorA.Run(ctx) }()
	go func() { _ = creatorB.Run(ctx) }()
	go func() { _ = joinerA.Run(ctx) }()
	go func() { _ = joinerB.Run(ctx) }()

	const n = 8 * 1024
	payloadA := bytes.Repeat([]byte("A"), n)
	payloadB := bytes.Repeat([]byte("B"), n)

	gotB := make(chan []byte, 1)
	go func() {
		_, conn, err := creatorB.Accept(ctx)
		if err != nil {
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		gotB <- buf
	}()
	go func() {
		_, conn, err := creatorA.Accept(ctx)
		if err != nil {
			return
		}
		buf := make([]byte, n)
		_, _ = io.ReadFull(conn, buf)
	}()

	connB, err := joinerB.Dial(ctx, "b.example:443")
	if err != nil {
		t.Fatal(err)
	}
	connA, err := joinerA.Dial(ctx, "a.example:443")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = connA.Write(payloadA) }()
	if _, err := connB.Write(payloadB); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-gotB:
		if !bytes.Equal(got, payloadB) {
			t.Fatal("joiner B payload mismatch")
		}
	case <-ctx.Done():
		t.Fatal("loss on A stalled B")
	}
}

func TestStaleEpochIgnored(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()

	got := make(chan []byte, 1)
	go func() {
		_, conn, err := creator.Accept(ctx)
		if err != nil {
			return
		}
		buf := make([]byte, 10)
		n, err := io.ReadFull(conn, buf)
		if err != nil {
			return
		}
		got <- buf[:n]
	}()

	conn, err := joiner.Dial(ctx, "ep.example:1")
	if err != nil {
		t.Fatal(err)
	}
	sc := conn.(*streamConn)
	stale, err := Pack(key, Frame{
		Type: TypeData, StreamID: sc.id, Dest: testSID(2), Src: testSID(1),
		Epoch: joiner.epoch + 1, Seq: 3, Payload: []byte("STALE!!!!!"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := left.Send(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("helloworld")); err != nil {
		t.Fatal(err)
	}
	select {
	case buf := <-got:
		if string(buf) != "helloworld" {
			t.Fatalf("stale epoch leaked: %q", buf)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
	cancel()
	left.Close()
	right.Close()
}

type filterCarrier struct {
	inner Carrier
	drop  func([]byte) bool
}

func (c *filterCarrier) Send(ctx context.Context, payload []byte) error {
	if c.drop != nil && c.drop(payload) {
		return nil
	}
	return c.inner.Send(ctx, payload)
}

type errOnceCarrier struct {
	inner Carrier
	fail  func([]byte) error
}

func (c *errOnceCarrier) Send(ctx context.Context, payload []byte) error {
	if c.fail != nil {
		if err := c.fail(payload); err != nil {
			return err
		}
	}
	return c.inner.Send(ctx, payload)
}

func (c *errOnceCarrier) Recv(ctx context.Context) ([]byte, error) {
	return c.inner.Recv(ctx)
}

func (c *filterCarrier) Recv(ctx context.Context) ([]byte, error) {
	return c.inner.Recv(ctx)
}

type lossyBus struct {
	inner   Carrier
	dropMod int
	n       atomic.Int32
}

func (c *lossyBus) Send(ctx context.Context, payload []byte) error {
	n := int(c.n.Add(1))
	if c.dropMod > 0 && n%c.dropMod == 0 {
		return nil
	}
	return c.inner.Send(ctx, payload)
}

func (c *lossyBus) Recv(ctx context.Context) ([]byte, error) {
	return c.inner.Recv(ctx)
}

func (s *streamConn) lastDataSeq() uint32 {
	s.mux.mu.Lock()
	defer s.mux.mu.Unlock()
	if s.mux.sendNext <= 1 {
		return 0
	}
	return s.mux.sendNext - 1
}

func (s *streamConn) bufferedLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf)
}

func TestFloodWithoutReadStaysBoundedAndBackpressures(t *testing.T) {
	left, right := newCarrierPair()
	left.maxQ = 1024
	right.maxQ = 1024
	joiner, creator, cancel := startMuxPair(t, left, right)
	defer cancel()
	defer left.Close()
	defer right.Close()

	ctx, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel2()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()

	conn, err := joiner.Dial(ctx, "bound.example:1")
	if err != nil {
		t.Fatal(err)
	}
	up := <-accepted
	if up == nil {
		t.Fatal("accept")
	}
	sc := up.(*streamConn)

	const flood = 256 * 1024
	done := make(chan error, 1)
	go func() {
		_, err := conn.Write(bytes.Repeat([]byte("x"), flood))
		done <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sc.bufferedLen() > StreamRecvCap {
			t.Fatalf("app recv buf %d exceeds StreamRecvCap %d", sc.bufferedLen(), StreamRecvCap)
		}
		left.mu.Lock()
		lq := len(left.q)
		left.mu.Unlock()
		right.mu.Lock()
		rq := len(right.q)
		right.mu.Unlock()
		if lq > 1024 || rq > 1024 {
			t.Fatalf("carrier queue unbounded left=%d right=%d", lq, rq)
		}
		creator.mu.Lock()
		rb := len(creator.recvBuf)
		creator.mu.Unlock()
		if rb > ARQWindow {
			t.Fatalf("recvBuf %d exceeds ARQWindow", rb)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := sc.bufferedLen(); n > StreamRecvCap {
		t.Fatalf("app recv buf %d exceeds cap %d", n, StreamRecvCap)
	}
	select {
	case err := <-done:
		t.Fatalf("256KiB write finished without Read (no backpressure), err=%v buf=%d", err, sc.bufferedLen())
	default:
	}

	got := make([]byte, flood)
	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(up, got)
		readDone <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write after reader: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sender did not unblock after Read")
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not finish")
	}
}

func TestPushUnblocksOnMuxClose(t *testing.T) {
	left, right := newCarrierPair()
	left.maxQ = 1024
	right.maxQ = 1024
	joiner, creator, cancel := startMuxPair(t, left, right)
	defer cancel()
	defer left.Close()
	defer right.Close()

	ctx, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel2()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	conn, err := joiner.Dial(ctx, "close.example:1")
	if err != nil {
		t.Fatal(err)
	}
	up := <-accepted
	if up == nil {
		t.Fatal("accept")
	}

	done := make(chan error, 1)
	go func() {
		_, err := conn.Write(bytes.Repeat([]byte("y"), 256*1024))
		done <- err
	}()
	time.Sleep(400 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("write finished before close (push never blocked): %v", err)
	default:
	}
	creator.Close()
	joiner.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write/push still blocked after mux close")
	}
}

func TestSendWindowZeroAdvertisementNotFullWhenIdle(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.gotPeerWnd = true
	m.peerRecvWnd = 0
	m.sendNext = 8
	m.sendUnacked = 8
	m.mu.Lock()
	full := m.sendWindowFullLocked()
	m.mu.Unlock()
	if full {
		t.Fatal("idle mux with wnd=0 must not report window full")
	}
}

func TestZeroWindowAckDoesNotDeadlockWrite(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()

	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, c)
	}()
	conn, err := joiner.Dial(ctx, "zw.example:1")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)

	joiner.mu.Lock()
	cum := joiner.sendNext
	joiner.mu.Unlock()
	ack, err := Pack(key, Frame{
		Type: TypeAck, Dest: testSID(1), Src: testSID(2),
		Epoch: creator.epoch, Payload: packAckPayload(cum, 0, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := right.Send(ctx, ack); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := conn.Write([]byte("z"))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("probe write after wnd=0: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wnd=0 ACK deadlocked Write (empty sendBuf)")
	}

	cancel()
	left.Close()
	right.Close()
}

func TestWriteCommittedDespiteFirstSendLoss(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	var dataSends atomic.Int32
	fail := &errOnceCarrier{inner: left, fail: func(p []byte) error {
		typ, _, _, ok := PeekRoute(p)
		if ok && typ == TypeData && dataSends.Add(1) == 1 {
			return io.ErrClosedPipe
		}
		return nil
	}}
	joiner := NewMux(key, fail)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()

	payload := bytes.Repeat([]byte("R"), MaxPayload)
	got := make(chan []byte, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			got <- nil
			return
		}
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(c, buf); err != nil {
			got <- nil
			return
		}
		got <- buf
	}()
	conn, err := joiner.Dial(ctx, "loss-send.example:1")
	if err != nil {
		t.Fatal(err)
	}
	n, err := conn.Write(payload)
	if err != nil {
		t.Fatalf("Write must treat committed Send failure as loss, got err=%v n=%d", err, n)
	}
	if n != len(payload) {
		t.Fatalf("Write n=%d want %d", n, len(payload))
	}
	select {
	case buf := <-got:
		if !bytes.Equal(buf, payload) {
			t.Fatalf("receiver bytes mismatch len=%d", len(buf))
		}
	case <-ctx.Done():
		t.Fatal("receiver did not get retransmitted data")
	}
	cancel()
	left.Close()
	right.Close()
}

func TestMuxPingSurvivesOneLoss(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	var pings atomic.Int32
	dropFirst := &filterCarrier{inner: left, drop: func(p []byte) bool {
		typ, _, _, ok := PeekRoute(p)
		return ok && typ == TypePing && pings.Add(1) == 1
	}}
	a := NewMux(key, dropFirst)
	a.SetRoute(testSID(1), testSID(2))
	b := NewMux(key, right)
	b.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = a.Run(ctx) }()
	go func() { _ = b.Run(ctx) }()
	if _, err := a.Ping(ctx); err != nil {
		t.Fatalf("Ping should retry after one loss: %v", err)
	}
	cancel()
	left.Close()
	right.Close()
}

func TestWriteWithoutDeadlineUnblocksOnNoProgress(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	creatorSend := &filterCarrier{inner: right, drop: func(p []byte) bool {
		typ, _, _, ok := PeekRoute(p)
		return ok && typ == TypeAck
	}}
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, creatorSend)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()

	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, c)
	}()
	conn, err := joiner.Dial(ctx, "stall.example:1")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := conn.Write(bytes.Repeat([]byte("s"), (ARQWindow+4)*MaxPayload))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("write succeeded while all ACKs dropped")
		}
		if d := time.Since(start); d > arqStallTimeout+3*time.Second {
			t.Fatalf("no-progress write took %s", d)
		}
	case <-time.After(arqStallTimeout + 4*time.Second):
		t.Fatal("Write without deadline waited forever")
	}
	cancel()
	left.Close()
	right.Close()
}

func waitStreamBuf(t *testing.T, sc *streamConn, min int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if sc.bufferedLen() >= min {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("stream buf %d want >= %d", sc.bufferedLen(), min)
}

func waitMuxOpen(t *testing.T, m *Mux) {
	t.Helper()
	if m.closed.Load() {
		t.Fatal("mux closed")
	}
}

func TestPushNonBlockingResults(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	sc := newStream(m, 1)
	if got := sc.push(bytes.Repeat([]byte("a"), StreamRecvCap)); got != pushAdmitted {
		t.Fatalf("cap fill: %v", got)
	}
	done := make(chan pushStatus, 1)
	go func() { done <- sc.push([]byte("x")) }()
	select {
	case got := <-done:
		if got != pushFull {
			t.Fatalf("want full, got %v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("push blocked waiting for space")
	}
	sc.remoteClose()
	if got := sc.push([]byte("y")); got != pushClosed {
		t.Fatalf("want closed, got %v", got)
	}
}

func TestClosedStreamInFlightDataDoesNotFreezeMux(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	connA, err := joiner.Dial(ctx, "a.example:1")
	if err != nil {
		t.Fatal(err)
	}
	upA := <-accepted
	if upA == nil {
		t.Fatal("accept A")
	}
	sid := connA.(*streamConn).id
	if _, err := connA.Write([]byte("sync")); err != nil {
		t.Fatal(err)
	}
	syncBuf := make([]byte, 4)
	if _, err := io.ReadFull(upA, syncBuf); err != nil {
		t.Fatal(err)
	}

	_ = connA.Close()
	_ = upA.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		joiner.mu.Lock()
		_, jok := joiner.streams[sid]
		jbuf := len(joiner.sendBuf)
		joiner.mu.Unlock()
		creator.mu.Lock()
		_, cok := creator.streams[sid]
		creator.mu.Unlock()
		if !jok && !cok && jbuf == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stream A did not close")
		}
		time.Sleep(5 * time.Millisecond)
	}

	var down atomic.Int64
	creator.SetTrafficHook(func(_, d int64) { down.Add(d) })

	creator.mu.Lock()
	seq := creator.recvNext
	creator.mu.Unlock()
	ghost := []byte("ghost-A")
	wire, err := Pack(key, Frame{
		Type: TypeData, Epoch: joiner.epoch, Seq: seq, StreamID: sid,
		Src: testSID(1), Dest: testSID(2), Payload: ghost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := left.Send(ctx, wire); err != nil {
		t.Fatal(err)
	}
	joiner.mu.Lock()
	if joiner.sendNext <= seq {
		joiner.sendNext = seq + 1
		joiner.sendUnacked = seq + 1
	}
	joiner.mu.Unlock()

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		creator.mu.Lock()
		next := creator.recvNext
		creator.mu.Unlock()
		if next > seq {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if down.Load() != 0 {
		t.Fatalf("discarded Data counted as download: %d", down.Load())
	}
	waitMuxOpen(t, creator)
	waitMuxOpen(t, joiner)

	acceptedB := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			acceptedB <- nil
			return
		}
		acceptedB <- c
	}()
	connB, err := joiner.Dial(ctx, "b.example:1")
	if err != nil {
		t.Fatal(err)
	}
	upB := <-acceptedB
	if upB == nil {
		t.Fatal("accept B: mux froze after in-flight Data for closed A")
	}
	msg := []byte("hello-B")
	if _, err := connB.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(upB, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("stream B payload %q", got)
	}
	if down.Load() != int64(len(msg)) {
		t.Fatalf("download %d want %d (B only, ghost discarded)", down.Load(), len(msg))
	}
	waitMuxOpen(t, creator)
	waitMuxOpen(t, joiner)
}

func TestFullStreamDoesNotBlockInboundControl(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	left.maxQ = 1024
	right.maxQ = 1024
	var acks, pongs atomic.Int64
	creatorSend := &peekSendCarrier{inner: right, onSend: func(p []byte) {
		typ, _, _, ok := PeekRoute(p)
		if !ok {
			return
		}
		switch typ {
		case TypeAck:
			acks.Add(1)
		case TypePong:
			pongs.Add(1)
		}
	}}
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, creatorSend)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	conn, err := joiner.Dial(ctx, "full.example:1")
	if err != nil {
		t.Fatal(err)
	}
	up := <-accepted
	if up == nil {
		t.Fatal("accept")
	}
	sc := up.(*streamConn)
	if _, err := conn.Write(bytes.Repeat([]byte("A"), StreamRecvCap)); err != nil {
		t.Fatal(err)
	}
	waitStreamBuf(t, sc, StreamRecvCap, 3*time.Second)

	pending := make(chan error, 1)
	go func() {
		_, err := conn.Write(bytes.Repeat([]byte("B"), (ARQWindow+1)*MaxPayload))
		pending <- err
	}()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-pending:
		t.Fatalf("pending Data finished while unread: %v", err)
	default:
	}

	acksBefore := acks.Load()
	pctx, pcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pcancel()
	if _, err := joiner.Ping(pctx); err != nil {
		t.Fatalf("Ping while stream full/pending: %v", err)
	}
	if pongs.Load() < 1 {
		t.Fatal("Pong not sent while Data pending")
	}

	wire, err := Pack(key, Frame{
		Type: TypeHello, Epoch: joiner.epoch, Seq: 1,
		Src: testSID(1), Dest: testSID(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := left.Send(ctx, wire); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if acks.Load() > acksBefore {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if acks.Load() <= acksBefore {
		t.Fatal("outbound ACK stalled while stream Data pending")
	}
	waitMuxOpen(t, creator)
	waitMuxOpen(t, joiner)
}

func TestRetransmitAdmittedOnceAfterReadFreesSpace(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	left.maxQ = 1024
	right.maxQ = 1024
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	var down atomic.Int64
	creator.SetTrafficHook(func(_, d int64) { down.Add(d) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	conn, err := joiner.Dial(ctx, "retry.example:1")
	if err != nil {
		t.Fatal(err)
	}
	up := <-accepted
	if up == nil {
		t.Fatal("accept")
	}
	sc := up.(*streamConn)
	fill := bytes.Repeat([]byte("A"), StreamRecvCap)
	if _, err := conn.Write(fill); err != nil {
		t.Fatal(err)
	}
	waitStreamBuf(t, sc, StreamRecvCap, 3*time.Second)
	downAtFull := down.Load()
	if downAtFull != int64(StreamRecvCap) {
		t.Fatalf("download at cap %d want %d", downAtFull, StreamRecvCap)
	}

	extra := bytes.Repeat([]byte("B"), MaxPayload)
	writeErr := make(chan error, 1)
	go func() {
		_, err := conn.Write(extra)
		writeErr <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if down.Load() != downAtFull {
		t.Fatalf("pending Data counted before admit: %d", down.Load())
	}

	gotFill := make([]byte, StreamRecvCap)
	if _, err := io.ReadFull(up, gotFill); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotFill, fill) {
		t.Fatal("fill mismatch")
	}
	gotExtra := make([]byte, MaxPayload)
	if _, err := io.ReadFull(up, gotExtra); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotExtra, extra) {
		t.Fatal("retransmitted pending Data mismatch")
	}
	select {
	case err := <-writeErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pending Write did not complete after Read")
	}
	if down.Load() != downAtFull+int64(MaxPayload) {
		t.Fatalf("download %d want %d (pending admitted once)", down.Load(), downAtFull+int64(MaxPayload))
	}

	tail := []byte("C-next")
	if _, err := conn.Write(tail); err != nil {
		t.Fatal(err)
	}
	gotTail := make([]byte, len(tail))
	if _, err := io.ReadFull(up, gotTail); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotTail, tail) {
		t.Fatalf("subsequent frame %q", gotTail)
	}
}

func TestCloseWhileFullPendingDoesNotKillMux(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	left.maxQ = 1024
	right.maxQ = 1024
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	connA, err := joiner.Dial(ctx, "full-close.example:1")
	if err != nil {
		t.Fatal(err)
	}
	upA := <-accepted
	if upA == nil {
		t.Fatal("accept A")
	}
	sc := upA.(*streamConn)
	if _, err := connA.Write(bytes.Repeat([]byte("A"), StreamRecvCap)); err != nil {
		t.Fatal(err)
	}
	waitStreamBuf(t, sc, StreamRecvCap, 3*time.Second)
	writeErr := make(chan error, 1)
	go func() {
		_, err := connA.Write(bytes.Repeat([]byte("B"), (ARQWindow+1)*MaxPayload))
		writeErr <- err
	}()
	time.Sleep(300 * time.Millisecond)

	if err := upA.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		waitMuxOpen(t, creator)
		waitMuxOpen(t, joiner)
		creator.mu.Lock()
		_, still := creator.streams[sc.id]
		creator.mu.Unlock()
		if !still {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	acceptedB := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			acceptedB <- nil
			return
		}
		acceptedB <- c
	}()
	connB, err := joiner.Dial(ctx, "after-full-close.example:1")
	if err != nil {
		t.Fatal(err)
	}
	upB := <-acceptedB
	if upB == nil {
		t.Fatal("accept B after close-while-full")
	}
	msg := []byte("stream-B-ok")
	if _, err := connB.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(upB, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("stream B %q", got)
	}
	waitMuxOpen(t, creator)
	waitMuxOpen(t, joiner)
}

func TestStreamFinTimeoutDoesNotCloseMux(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	left.maxQ = 1024
	right.maxQ = 1024
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	conn, err := joiner.Dial(ctx, "fin-stall.example:1")
	if err != nil {
		t.Fatal(err)
	}
	up := <-accepted
	if up == nil {
		t.Fatal("accept")
	}
	sc := up.(*streamConn)
	if _, err := conn.Write(bytes.Repeat([]byte("A"), StreamRecvCap)); err != nil {
		t.Fatal(err)
	}
	waitStreamBuf(t, sc, StreamRecvCap, 3*time.Second)
	go func() {
		_, _ = conn.Write(bytes.Repeat([]byte("B"), (ARQWindow+2)*MaxPayload))
	}()
	time.Sleep(300 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- conn.Close() }()
	time.Sleep(400 * time.Millisecond)
	waitMuxOpen(t, joiner)
	waitMuxOpen(t, creator)

	pctx, pcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pcancel()
	if _, err := joiner.Ping(pctx); err != nil {
		t.Fatalf("Ping during Fin window wait: %v", err)
	}
	waitMuxOpen(t, joiner)
	waitMuxOpen(t, creator)
	select {
	case <-closed:
	default:
	}
}

type peekSendCarrier struct {
	inner  Carrier
	onSend func([]byte)
}

func (c *peekSendCarrier) Send(ctx context.Context, p []byte) error {
	if c.onSend != nil {
		c.onSend(p)
	}
	return c.inner.Send(ctx, p)
}

func (c *peekSendCarrier) Recv(ctx context.Context) ([]byte, error) {
	return c.inner.Recv(ctx)
}

func waitCond(t *testing.T, d time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestFailedAdmitNeverDeletesRecvBuf(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer m.Close()
	go m.drainLoop(ctx)
	sc := newStream(m, 1)
	m.streams[1] = sc
	if sc.push(bytes.Repeat([]byte("A"), StreamRecvCap)) != pushAdmitted {
		t.Fatal("fill")
	}
	pktN := bytes.Repeat([]byte("B"), MaxPayload)
	pktN1 := bytes.Repeat([]byte("C"), MaxPayload)
	m.recvNext = 5
	m.recvBuf[5] = Frame{Type: TypeData, Seq: 5, StreamID: 1, Payload: pktN}
	m.recvBuf[6] = Frame{Type: TypeData, Seq: 6, StreamID: 1, Payload: pktN1}

	m.recvReliable(ctx, Frame{Type: TypeData, Seq: 5, StreamID: 1, Payload: pktN})
	if m.recvNext != 5 {
		t.Fatalf("recvNext advanced past unadmitted N: %d", m.recvNext)
	}
	if _, ok := m.recvBuf[5]; !ok {
		t.Fatal("failed admit deleted recvBuf[N]")
	}
	if _, ok := m.recvBuf[6]; !ok {
		t.Fatal("failed admit deleted recvBuf[N+1]")
	}

	got := make([]byte, MaxPayload)
	if n, err := sc.Read(got); err != nil || n != MaxPayload {
		t.Fatalf("read space: n=%d err=%v", n, err)
	}
	waitCond(t, 2*time.Second, "Read did not auto-admit retained N", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.recvNext == 6
	})
	m.mu.Lock()
	_, hasN1 := m.recvBuf[6]
	_, hasN := m.recvBuf[5]
	m.mu.Unlock()
	if !hasN1 {
		t.Fatal("pushFull on N+1 deleted recvBuf[N+1]")
	}
	if hasN {
		t.Fatal("admitted N still in recvBuf")
	}

	if n, err := sc.Read(got); err != nil || n != MaxPayload {
		t.Fatalf("read space for N+1: n=%d err=%v", n, err)
	}
	waitCond(t, 2*time.Second, "Read did not auto-admit retained N+1 (no inject)", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.recvNext == 7 && len(m.recvBuf) == 0
	})
	rest := make([]byte, StreamRecvCap-2*MaxPayload+2*MaxPayload)
	if _, err := io.ReadFull(sc, rest); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("A"), StreamRecvCap-2*MaxPayload)
	want = append(want, pktN...)
	want = append(want, pktN1...)
	if !bytes.Equal(rest, want) {
		t.Fatal("N/N+1 not delivered in order from retained frames")
	}
}

func TestSackedOOONotLostAfterPartialReadAdmit(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	left.maxQ = 1024
	right.maxQ = 1024
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	conn, err := joiner.Dial(ctx, "sack-loss.example:1")
	if err != nil {
		t.Fatal(err)
	}
	up := <-accepted
	if up == nil {
		t.Fatal("accept")
	}
	sc := up.(*streamConn)
	fill := bytes.Repeat([]byte("A"), StreamRecvCap)
	pktN := bytes.Repeat([]byte("B"), MaxPayload)
	pktN1 := bytes.Repeat([]byte("C"), MaxPayload)
	if _, err := conn.Write(fill); err != nil {
		t.Fatal(err)
	}
	waitStreamBuf(t, sc, StreamRecvCap, 3*time.Second)

	creator.mu.Lock()
	nSeq := creator.recvNext
	creator.mu.Unlock()
	n1Seq := nSeq + 1

	if _, err := conn.Write(append(append([]byte{}, pktN...), pktN1...)); err != nil {
		t.Fatal(err)
	}
	waitCond(t, 3*time.Second, "N+1 not SACKed/retained", func() bool {
		creator.mu.Lock()
		_, hasN1 := creator.recvBuf[n1Seq]
		_, hasN := creator.recvBuf[nSeq]
		creator.mu.Unlock()
		joiner.mu.Lock()
		_, n1Inflight := joiner.sendBuf[n1Seq]
		joiner.mu.Unlock()
		return hasN && hasN1 && !n1Inflight
	})

	got := make([]byte, MaxPayload)
	if _, err := io.ReadFull(up, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bytes.Repeat([]byte("A"), MaxPayload)) {
		t.Fatal("first Read was not fill head")
	}

	waitCond(t, 2*time.Second, "after N admit: N+1 must stay on pushFull (no inject)", func() bool {
		creator.mu.Lock()
		defer creator.mu.Unlock()
		_, hasN1 := creator.recvBuf[n1Seq]
		_, hasN := creator.recvBuf[nSeq]
		return creator.recvNext == n1Seq && hasN1 && !hasN
	})

	if _, err := io.ReadFull(up, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bytes.Repeat([]byte("A"), MaxPayload)) {
		t.Fatal("second Read was not fill head")
	}

	waitCond(t, 2*time.Second, "N+1 not auto-admitted after Read (no N+1 inject)", func() bool {
		creator.mu.Lock()
		cNext, nbuf := creator.recvNext, len(creator.recvBuf)
		creator.mu.Unlock()
		joiner.mu.Lock()
		unacked := joiner.sendUnacked
		_, nSlot := joiner.sendBuf[nSeq]
		_, n1Slot := joiner.sendBuf[n1Seq]
		joiner.mu.Unlock()
		return cNext > n1Seq && nbuf == 0 && unacked > n1Seq && !nSlot && !n1Slot
	})

	rest := make([]byte, StreamRecvCap-2*MaxPayload+2*MaxPayload)
	if _, err := io.ReadFull(up, rest); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("A"), StreamRecvCap-2*MaxPayload)
	want = append(want, pktN...)
	want = append(want, pktN1...)
	if !bytes.Equal(rest, want) {
		t.Fatal("N and N+1 not delivered exactly once in order")
	}
	waitMuxOpen(t, creator)
	waitMuxOpen(t, joiner)
	creator.mu.Lock()
	rb := len(creator.recvBuf)
	creator.mu.Unlock()
	if rb != 0 {
		t.Fatalf("recvBuf not empty: %d", rb)
	}
}

func TestConcurrentReadsDrainRetainedHeadOnce(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	left.maxQ = 1024
	right.maxQ = 1024
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	conn, err := joiner.Dial(ctx, "drain-race.example:1")
	if err != nil {
		t.Fatal(err)
	}
	up := <-accepted
	if up == nil {
		t.Fatal("accept")
	}
	sc := up.(*streamConn)
	fill := bytes.Repeat([]byte("A"), StreamRecvCap)
	pktN := bytes.Repeat([]byte("B"), MaxPayload)
	pktN1 := bytes.Repeat([]byte("C"), MaxPayload)
	if _, err := conn.Write(fill); err != nil {
		t.Fatal(err)
	}
	waitStreamBuf(t, sc, StreamRecvCap, 3*time.Second)
	creator.mu.Lock()
	nSeq := creator.recvNext
	creator.mu.Unlock()
	n1Seq := nSeq + 1
	if _, err := conn.Write(append(append([]byte{}, pktN...), pktN1...)); err != nil {
		t.Fatal(err)
	}
	waitCond(t, 3*time.Second, "N+1 not SACKed/retained", func() bool {
		creator.mu.Lock()
		_, hasN1 := creator.recvBuf[n1Seq]
		creator.mu.Unlock()
		joiner.mu.Lock()
		_, n1Inflight := joiner.sendBuf[n1Seq]
		joiner.mu.Unlock()
		return hasN1 && !n1Inflight
	})

	var wg sync.WaitGroup
	got := [2][]byte{make([]byte, MaxPayload), make([]byte, MaxPayload)}
	errs := [2]error{}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errs[i] = io.ReadFull(up, got[i])
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent read %d: %v", i, err)
		}
		if !bytes.Equal(got[i], bytes.Repeat([]byte("A"), MaxPayload)) {
			t.Fatalf("concurrent read %d was not fill head", i)
		}
	}
	waitCond(t, 2*time.Second, "concurrent Read did not admit N and N+1 once", func() bool {
		creator.mu.Lock()
		defer creator.mu.Unlock()
		return creator.recvNext > n1Seq && len(creator.recvBuf) == 0
	})
	rest := make([]byte, StreamRecvCap-2*MaxPayload+2*MaxPayload)
	if _, err := io.ReadFull(up, rest); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("A"), StreamRecvCap-2*MaxPayload)
	want = append(want, pktN...)
	want = append(want, pktN1...)
	if !bytes.Equal(rest, want) {
		t.Fatal("duplicate or lost retained frames under concurrent Read")
	}
	waitMuxOpen(t, creator)
	waitMuxOpen(t, joiner)
}
