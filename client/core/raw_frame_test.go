package core

import (
	"testing"
	"time"
)

func TestRawFrameRoundTrip(t *testing.T) {
	ip := make([]byte, 40)
	ip[0] = 0x45
	framed := rawFrameEncode(42, ip, nil)
	seq, got, ok := rawFrameDecode(framed)
	if !ok || seq != 42 || len(got) != 40 || got[0] != 0x45 {
		t.Fatalf("decode fail ok=%v seq=%d len=%d", ok, seq, len(got))
	}
}

func TestRawReorderInOrder(t *testing.T) {
	r := newRawReorder()
	p0 := []byte{0x45, 0}
	p1 := []byte{0x45, 1}
	p2 := []byte{0x45, 2}
	out := r.Push(0, p0)
	if len(out) != 1 || out[0][1] != 0 {
		t.Fatalf("first: %#v", out)
	}
	out = r.Push(2, p2)
	if len(out) != 0 {
		t.Fatalf("gap must wait (no skip), got %d", len(out))
	}
	out = r.Push(1, p1)
	if len(out) != 2 || out[0][1] != 1 || out[1][1] != 2 {
		t.Fatalf("want 1 then 2, got %#v", out)
	}
}

func TestRawReorderFlushesStalledTailWithoutNewPacket(t *testing.T) {
	r := newRawReorder()
	p0 := []byte{0x45, 0}
	p2 := []byte{0x45, 2}
	if out := r.Push(0, p0); len(out) != 1 {
		t.Fatalf("first: %#v", out)
	}
	if out := r.Push(2, p2); len(out) != 0 {
		t.Fatalf("gap must wait, got %#v", out)
	}
	time.Sleep(rawReorderStallTTL + 10*time.Millisecond)
	out := r.FlushExpired()
	if len(out) != 1 || out[0][1] != 2 {
		t.Fatalf("timer must release stalled tail, got %#v", out)
	}
}
