package wbtunnel

import (
	"encoding/binary"
	"sync"
	"time"
)

const (
	frameSeqLen   = 4
	frameHoldMax  = 128
	frameSeqStart = 1
	// frameHoldTime bounds how long a missing carrier frame may stall the
	// downlink. Carrier VP8 frames are lossy and never retransmitted at the
	// carrier layer, so once a gap ages past this we skip it and let KCP (which
	// runs above and has its own retransmit/ordering) recover the lost chunk.
	// With redundant dual-track fan-out this mainly covers rare same-seq loss
	// on both tracks; keep it short so a true gap does not stall SOCKS long.
	frameHoldTime = 80 * time.Millisecond
)

// frameReorder buffers dual-track VP8 frames until sequential delivery.
type frameReorder struct {
	mu        sync.Mutex
	next      uint32
	started   bool
	blockedAt time.Time
	pending   map[uint32][]byte
}

func newFrameReorder() *frameReorder {
	return &frameReorder{
		next:    frameSeqStart,
		pending: make(map[uint32][]byte, 8),
	}
}

func (f *frameReorder) ingest(data []byte) (ready [][]byte) {
	if len(data) < frameSeqLen {
		return nil
	}
	seq := binary.BigEndian.Uint32(data[0:frameSeqLen])
	payload := append([]byte(nil), data[frameSeqLen:]...)

	f.mu.Lock()
	defer f.mu.Unlock()

	// The sender's sequence counter is not reset on KCP/smux restart, but the
	// receiver's reorder buffer is recreated with next=1. Sync to the first
	// frame we actually see so a stale next never strands the whole downlink.
	if !f.started {
		f.started = true
		f.next = seq
	}

	// Sender restarted its counter (or a huge gap): resync instead of stranding
	// every frame in pending until frameHoldMax evicts them.
	if seq > f.next+frameHoldMax {
		f.pending = make(map[uint32][]byte, 8)
		f.next = seq
		f.blockedAt = time.Time{}
	}

	if seq < f.next {
		return nil
	}
	if seq > f.next {
		if len(f.pending) >= frameHoldMax {
			// Drop oldest held frame to bound memory under severe reorder.
			var drop uint32
			for s := range f.pending {
				if drop == 0 || s < drop {
					drop = s
				}
			}
			delete(f.pending, drop)
		}
		f.pending[seq] = payload
		if f.blockedAt.IsZero() {
			f.blockedAt = time.Now()
		}
		// A lost carrier frame is never retransmitted at the carrier layer, so
		// don't stall the whole downlink waiting for it — skip the gap once it
		// ages out and let KCP recover the missing chunk.
		if time.Since(f.blockedAt) >= frameHoldTime {
			return f.flushGapLocked()
		}
		return nil
	}

	f.next++
	return f.drainLocked(payload)
}

// flushGapLocked advances past a stalled gap to the smallest buffered frame and
// drains what is now contiguous. Caller holds f.mu and pending is non-empty.
func (f *frameReorder) flushGapLocked() [][]byte {
	var min uint32
	first := true
	for s := range f.pending {
		if first || s < min {
			min = s
			first = false
		}
	}
	f.next = min
	return f.drainLocked(nil)
}

// drainLocked emits head (if any) followed by every contiguous buffered frame
// starting at f.next, then updates the stall timer. Caller holds f.mu.
func (f *frameReorder) drainLocked(head []byte) [][]byte {
	var out [][]byte
	if head != nil {
		out = append(out, head)
	}
	for {
		p, ok := f.pending[f.next]
		if !ok {
			break
		}
		delete(f.pending, f.next)
		f.next++
		out = append(out, p)
	}
	if len(f.pending) == 0 {
		f.blockedAt = time.Time{}
	} else {
		// A new gap remains at f.next — restart the stall timer for it.
		f.blockedAt = time.Now()
	}
	return out
}
