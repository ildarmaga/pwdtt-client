package tunnel

import (
	"bytes"
	"testing"
)

func newTestVP8Tunnel() *VP8DataTunnel {
	return NewVP8DataTunnel(nil, nil, func(string, ...any) {})
}

func TestCoalesceFromQueueSinglePacket(t *testing.T) {
	tun := newTestVP8Tunnel()
	tun.sendQueue <- []byte("one")

	got := tun.coalesceFromQueue(64)
	if !bytes.Equal(got, []byte("one")) {
		t.Fatalf("got %q want one", got)
	}
	if tun.coalesceFromQueue(64) != nil {
		t.Fatal("expected empty queue")
	}
}

func TestCoalesceFromQueueMergesUpToBatch(t *testing.T) {
	tun := newTestVP8Tunnel()
	tun.sendQueue <- []byte("aa")
	tun.sendQueue <- []byte("bb")
	tun.sendQueue <- []byte("cc")

	got := tun.coalesceFromQueue(2)
	if !bytes.Equal(got, []byte("aabb")) {
		t.Fatalf("got %q want aabb", got)
	}
	if tun.coalesceFromQueue(64) == nil {
		t.Fatal("expected cc still queued")
	}
}

func TestCoalesceFromQueueRespectsMaxPlaintext(t *testing.T) {
	tun := newTestVP8Tunnel()
	big := bytes.Repeat([]byte("x"), maxCoalescePlain-4)
	tail := []byte("overflow") // 8 bytes; (maxCoalescePlain-4)+8 > maxCoalescePlain
	tun.sendQueue <- big
	tun.sendQueue <- tail

	got := tun.coalesceFromQueue(64)
	if !bytes.Equal(got, big) {
		t.Fatalf("first chunk len=%d want %d", len(got), len(big))
	}
	tun.pendingMu.Lock()
	pending := tun.pendingCoalesce
	tun.pendingMu.Unlock()
	if !bytes.Equal(pending, tail) {
		t.Fatalf("pending=%q want overflow", pending)
	}

	got2 := tun.coalesceFromQueue(64)
	if !bytes.Equal(got2, tail) {
		t.Fatalf("second read=%q want overflow", got2)
	}
}

func TestCoalesceFromQueueDrainsPendingFirst(t *testing.T) {
	tun := newTestVP8Tunnel()
	tun.pendingCoalesce = []byte("pending")
	tun.sendQueue <- []byte("fresh")

	got := tun.coalesceFromQueue(64)
	if !bytes.Equal(got, []byte("pendingfresh")) {
		t.Fatalf("got %q want pendingfresh", got)
	}
}
