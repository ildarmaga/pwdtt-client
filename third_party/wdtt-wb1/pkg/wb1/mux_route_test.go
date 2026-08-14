package wb1

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

type broadcastBus struct {
	mu   sync.Mutex
	subs []*busEndpoint
}

type busEndpoint struct {
	bus    *broadcastBus
	local  SessionID
	remote SessionID
	mu     sync.Mutex
	q      [][]byte
	wait   chan struct{}
	dead   bool
}

func newBroadcastBus() *broadcastBus {
	return &broadcastBus{}
}

func (b *broadcastBus) endpoint(local, remote SessionID) *busEndpoint {
	e := &busEndpoint{bus: b, local: local, remote: remote, wait: make(chan struct{}, 1)}
	b.mu.Lock()
	b.subs = append(b.subs, e)
	b.mu.Unlock()
	return e
}

func (e *busEndpoint) Send(_ context.Context, payload []byte) error {
	e.bus.deliver(append([]byte(nil), payload...))
	return nil
}

func (e *busEndpoint) Recv(ctx context.Context) ([]byte, error) {
	for {
		e.mu.Lock()
		if len(e.q) > 0 {
			p := e.q[0]
			e.q = e.q[1:]
			e.mu.Unlock()
			return p, nil
		}
		if e.dead {
			e.mu.Unlock()
			return nil, io.EOF
		}
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.wait:
		}
	}
}

func (e *busEndpoint) push(p []byte) {
	e.mu.Lock()
	e.q = append(e.q, p)
	select {
	case e.wait <- struct{}{}:
	default:
	}
	e.mu.Unlock()
}

func (b *broadcastBus) deliver(payload []byte) {
	_, dest, src, ok := PeekRoute(payload)
	if !ok {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.subs {
		if e.local == dest && e.remote == src {
			e.push(payload)
		}
	}
}

func TestTwoJoinersSameStreamIDIsolated(t *testing.T) {
	key := testKey(t)
	creatorSID, aSID, bSID := testSID(1), testSID(2), testSID(3)
	bus := newBroadcastBus()

	creatorA := NewMux(key, bus.endpoint(creatorSID, aSID))
	creatorA.SetRoute(creatorSID, aSID)
	creatorB := NewMux(key, bus.endpoint(creatorSID, bSID))
	creatorB.SetRoute(creatorSID, bSID)
	joinerA := NewMux(key, bus.endpoint(aSID, creatorSID))
	joinerA.SetRoute(aSID, creatorSID)
	joinerB := NewMux(key, bus.endpoint(bSID, creatorSID))
	joinerB.SetRoute(bSID, creatorSID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = creatorA.Run(ctx) }()
	go func() { _ = creatorB.Run(ctx) }()
	go func() { _ = joinerA.Run(ctx) }()
	go func() { _ = joinerB.Run(ctx) }()

	errCh := make(chan error, 4)
	acceptA := make(chan struct{})
	acceptB := make(chan struct{})
	go func() {
		close(acceptA)
		dest, conn, err := creatorA.Accept(ctx)
		if err != nil {
			errCh <- err
			return
		}
		if dest != "a.example:443" {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		buf := make([]byte, 7)
		n, err := io.ReadFull(conn, buf)
		if err != nil {
			errCh <- err
			return
		}
		if string(buf[:n]) != "hello-A" {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		_, err = conn.Write([]byte("pong-A"))
		errCh <- err
	}()
	go func() {
		close(acceptB)
		dest, conn, err := creatorB.Accept(ctx)
		if err != nil {
			errCh <- err
			return
		}
		if dest != "b.example:443" {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		buf := make([]byte, 7)
		n, err := io.ReadFull(conn, buf)
		if err != nil {
			errCh <- err
			return
		}
		if string(buf[:n]) != "hello-B" {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		_, err = conn.Write([]byte("pong-B"))
		errCh <- err
	}()

	select {
	case <-acceptA:
	case <-ctx.Done():
		t.Fatal("accept A not started")
	}
	select {
	case <-acceptB:
	case <-ctx.Done():
		t.Fatal("accept B not started")
	}
	time.Sleep(20 * time.Millisecond)

	connA, err := joinerA.Dial(ctx, "a.example:443")
	if err != nil {
		t.Fatal(err)
	}
	connB, err := joinerB.Dial(ctx, "b.example:443")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connA.Write([]byte("hello-A")); err != nil {
		t.Fatal(err)
	}
	if _, err := connB.Write([]byte("hello-B")); err != nil {
		t.Fatal(err)
	}

	replyA := make([]byte, 6)
	if _, err := io.ReadFull(connA, replyA); err != nil {
		t.Fatal(err)
	}
	if string(replyA) != "pong-A" {
		t.Fatalf("joiner A got %q", replyA)
	}
	replyB := make([]byte, 6)
	if _, err := io.ReadFull(connB, replyB); err != nil {
		t.Fatal(err)
	}
	if string(replyB) != "pong-B" {
		t.Fatalf("joiner B got %q", replyB)
	}

	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("creator accept timed out")
		}
	}
}

func TestJoinerIgnoresFrameForOtherSID(t *testing.T) {
	key := testKey(t)
	me, other, creator := testSID(10), testSID(11), testSID(1)
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	joiner.SetRoute(me, creator)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()

	foreign, err := Pack(key, Frame{Type: TypeData, StreamID: 2, Dest: other, Src: creator, Payload: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if err := right.Send(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	mine, err := Pack(key, Frame{Type: TypeOpen, StreamID: 2, Dest: me, Src: creator, Seq: 1, Payload: []byte("ok:1")})
	if err != nil {
		t.Fatal(err)
	}
	if err := right.Send(ctx, mine); err != nil {
		t.Fatal(err)
	}

	dest, conn, err := joiner.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dest != "ok:1" {
		t.Fatalf("dest %q", dest)
	}
	_ = conn.Close()
	cancel()
	left.Close()
	right.Close()
}

func TestMuxDoesNotDuplicateDataSend(t *testing.T) {
	key := testKey(t)
	var n int
	c := &countCarrier{send: func(p []byte) error {
		n++
		if !bytes.Contains(p, Magic[:]) {
			return nil
		}
		typ, dest, _, ok := PeekRoute(p)
		if !ok || typ != TypeData || dest.IsZero() {
			t.Fatalf("data send typ peek ok=%v", ok)
		}
		return nil
	}}
	m := NewMux(key, c)
	m.SetRoute(testSID(1), testSID(2))
	if err := m.send(context.Background(), Frame{Type: TypeData, StreamID: 1, Payload: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("data sent %d times, want 1", n)
	}
}

type countCarrier struct {
	send func([]byte) error
}

func (c *countCarrier) Send(_ context.Context, payload []byte) error {
	return c.send(payload)
}

func (c *countCarrier) Recv(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
