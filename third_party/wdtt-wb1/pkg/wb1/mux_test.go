package wb1

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

type memCarrier struct {
	mu   sync.Mutex
	peer *memCarrier
	q    [][]byte
	wait chan struct{}
	dead bool
}

func newCarrierPair() (*memCarrier, *memCarrier) {
	a := &memCarrier{wait: make(chan struct{}, 1)}
	b := &memCarrier{wait: make(chan struct{}, 1)}
	a.peer = b
	b.peer = a
	return a, b
}

func (c *memCarrier) Send(_ context.Context, payload []byte) error {
	cp := append([]byte(nil), payload...)
	c.peer.mu.Lock()
	if c.peer.dead {
		c.peer.mu.Unlock()
		return io.ErrClosedPipe
	}
	c.peer.q = append(c.peer.q, cp)
	select {
	case c.peer.wait <- struct{}{}:
	default:
	}
	c.peer.mu.Unlock()
	return nil
}

func (c *memCarrier) Recv(ctx context.Context) ([]byte, error) {
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

func (c *memCarrier) Close() {
	c.mu.Lock()
	c.dead = true
	select {
	case c.wait <- struct{}{}:
	default:
	}
	c.mu.Unlock()
}

func TestMuxDialAcceptCopyFin(t *testing.T) {
	key, err := DeriveKey("secret", "room-mux")
	if err != nil {
		t.Fatal(err)
	}
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 2)
	go func() { errCh <- joiner.Run(ctx) }()
	go func() { errCh <- creator.Run(ctx) }()

	gotCh := make(chan string, 1)
	go func() {
		dest, conn, err := creator.Accept(ctx)
		if err != nil {
			errCh <- err
			return
		}
		gotCh <- dest
		buf := make([]byte, 64)
		n, err := io.ReadFull(conn, buf[:5])
		if err != nil {
			errCh <- err
			return
		}
		if _, err := conn.Write([]byte("pong")); err != nil {
			errCh <- err
			return
		}
		_ = conn.Close()
		if string(buf[:n]) != "hello" {
			errCh <- io.ErrUnexpectedEOF
		}
	}()

	conn, err := joiner.Dial(ctx, "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "pong" {
		t.Fatalf("reply %q", reply)
	}
	_ = conn.Close()

	select {
	case dest := <-gotCh:
		if dest != "example.com:443" {
			t.Fatalf("dest %q", dest)
		}
	case <-ctx.Done():
		t.Fatal("accept timed out")
	}

	cancel()
	left.Close()
	right.Close()
}

func TestMuxPing(t *testing.T) {
	key, err := DeriveKey("secret", "room-ping")
	if err != nil {
		t.Fatal(err)
	}
	left, right := newCarrierPair()
	a := NewMux(key, left)
	a.SetRoute(testSID(1), testSID(2))
	b := NewMux(key, right)
	b.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _ = a.Run(ctx) }()
	go func() { _ = b.Run(ctx) }()

	rtt, err := a.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rtt < 0 {
		t.Fatalf("negative rtt %s", rtt)
	}
	cancel()
	left.Close()
	right.Close()
}

func TestMuxRejectsOversizedPayload(t *testing.T) {
	key, err := DeriveKey("secret", "room-big")
	if err != nil {
		t.Fatal(err)
	}
	huge := bytes.Repeat([]byte("x"), MaxPayload+1)
	if _, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: testSID(1), Src: testSID(2), Payload: huge}); err == nil {
		t.Fatal("oversized payload must fail pack")
	}
}
