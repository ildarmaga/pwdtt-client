package wbtunnel

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestFrameReorderInOrder(t *testing.T) {
	r := newFrameReorder()
	p1 := []byte{1, 2, 3}
	p2 := []byte{4, 5}
	out1 := r.ingest(encodeSeq(1, p1))
	if len(out1) != 1 || string(out1[0]) != string(p1) {
		t.Fatalf("seq1: got %v", out1)
	}
	out2 := r.ingest(encodeSeq(2, p2))
	if len(out2) != 1 || string(out2[0]) != string(p2) {
		t.Fatalf("seq2: got %v", out2)
	}
}

func TestFrameReorderOutOfOrder(t *testing.T) {
	r := newFrameReorder()
	// Establish the stream baseline (first frame syncs next lazily).
	if got := r.ingest(encodeSeq(1, []byte{1})); len(got) != 1 {
		t.Fatalf("expected seq1 delivered, got %v", got)
	}
	p2 := []byte{2}
	p3 := []byte{3}
	if got := r.ingest(encodeSeq(3, p3)); len(got) != 0 {
		t.Fatalf("expected hold seq3, got %v", got)
	}
	got := r.ingest(encodeSeq(2, p2))
	if len(got) != 2 {
		t.Fatalf("expected 2 ready, got %d", len(got))
	}
}

// TestFrameReorderLazyStart verifies the buffer syncs to whatever sequence the
// sender is currently at (its counter is not reset on KCP restart), so a fresh
// reorder with next=1 does not strand the entire downlink.
func TestFrameReorderLazyStart(t *testing.T) {
	r := newFrameReorder()
	got := r.ingest(encodeSeq(5000, []byte{9}))
	if len(got) != 1 || got[0][0] != 9 {
		t.Fatalf("expected first frame delivered regardless of seq, got %v", got)
	}
	if out := r.ingest(encodeSeq(5001, []byte{10})); len(out) != 1 || out[0][0] != 10 {
		t.Fatalf("expected seq5001 delivered, got %v", out)
	}
}

// TestFrameReorderSenderRestart verifies a large forward jump (sender counter
// reset) resyncs instead of stranding frames in pending.
func TestFrameReorderSenderRestart(t *testing.T) {
	r := newFrameReorder()
	if got := r.ingest(encodeSeq(10, []byte{1})); len(got) != 1 {
		t.Fatalf("baseline: got %v", got)
	}
	// Jump far beyond next+frameHoldMax → resync.
	if got := r.ingest(encodeSeq(10+frameHoldMax+50, []byte{2})); len(got) != 1 {
		t.Fatalf("expected resync delivery after big jump, got %v", got)
	}
}

// TestFrameReorderLossyFlush verifies that a lost carrier frame (never
// retransmitted at the carrier layer) does not stall the downlink: once the gap
// ages past frameHoldTime, later frames are flushed so KCP above can recover.
func TestFrameReorderLossyFlush(t *testing.T) {
	r := newFrameReorder()
	if got := r.ingest(encodeSeq(1, []byte{1})); len(got) != 1 {
		t.Fatalf("baseline: got %v", got)
	}
	// seq2 is lost. seq3 arrives and is held (gap at next=2).
	if got := r.ingest(encodeSeq(3, []byte{3})); len(got) != 0 {
		t.Fatalf("expected hold seq3, got %v", got)
	}
	// Age the gap past the hold window.
	r.mu.Lock()
	r.blockedAt = time.Now().Add(-2 * frameHoldTime)
	r.mu.Unlock()
	// seq4 arrives: gap is stale → flush past the lost seq2.
	got := r.ingest(encodeSeq(4, []byte{4}))
	if len(got) != 2 || got[0][0] != 3 || got[1][0] != 4 {
		t.Fatalf("expected flush of seq3,seq4 past lost seq2, got %v", got)
	}
	// Recovery: seq5 delivered in order after the flush advanced next.
	if out := r.ingest(encodeSeq(5, []byte{5})); len(out) != 1 || out[0][0] != 5 {
		t.Fatalf("expected seq5 delivered after flush, got %v", out)
	}
}

func encodeSeq(seq uint32, payload []byte) []byte {
	out := make([]byte, frameSeqLen+len(payload))
	binary.BigEndian.PutUint32(out[:frameSeqLen], seq)
	copy(out[frameSeqLen:], payload)
	return out
}
