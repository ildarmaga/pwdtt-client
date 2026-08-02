package wbtunnel

import "testing"

// dualPipe wraps pipeTunnel with a configurable sub-tunnel count so it satisfies
// dualTrackSender (SubTunnelCount). Dual VP8 is fingerprint-only: KCP must NOT
// enable seq reorder, or the first 4 payload bytes are stripped and smux dies.
type dualPipe struct {
	*pipeTunnel
	subs int
}

func (d *dualPipe) SubTunnelCount() int { return d.subs }

func linkReorderEnabled(t *testing.T, subs int) bool {
	t.Helper()
	a, _ := newPipePair()
	l := NewLink(&dualPipe{pipeTunnel: a, subs: subs}, nil)
	if err := l.Attach(nil); err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer l.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reorder != nil
}

// TestLinkNeverReordersDualFingerprint locks the camera-only invariant:
// SubTunnelCount=2 must NOT enable frame reorder (no seq prefix on the wire).
func TestLinkNeverReordersDualFingerprint(t *testing.T) {
	if linkReorderEnabled(t, 2) {
		t.Fatal("dual-track fingerprint carrier must not enable frame reorder")
	}
	if linkReorderEnabled(t, 1) {
		t.Fatal("single-track carrier must not enable frame reorder")
	}
}

// TestLinkRebindKeepsNoReorder verifies rebind does not turn reorder back on.
func TestLinkRebindKeepsNoReorder(t *testing.T) {
	a, _ := newPipePair()
	l := NewLink(&dualPipe{pipeTunnel: a, subs: 2}, nil)
	if err := l.Attach(nil); err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer l.Close()

	b, _ := newPipePair()
	if err := l.Rebind(&dualPipe{pipeTunnel: b, subs: 2}, nil); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	l.mu.Lock()
	r := l.reorder
	l.mu.Unlock()
	if r != nil {
		t.Fatal("rebind to dual-track fingerprint must keep reorder disabled")
	}
}
