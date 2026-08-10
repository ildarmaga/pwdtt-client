package core

import "testing"

func TestCredBootstrapMinSlots(t *testing.T) {
	if got := credBootstrapMinSlots(4); got != 1 {
		t.Fatalf("pool=4 bootstrap=%d want 1", got)
	}
	if got := credBootstrapMinSlots(0); got != 1 {
		t.Fatalf("pool=0 bootstrap=%d want 1", got)
	}
	if got := credBootstrapMinSlots(2); got != 1 {
		t.Fatalf("pool=2 bootstrap=%d want 1", got)
	}
}

func TestPickReadyCredSlot(t *testing.T) {
	ready := []bool{true, false, false, false}
	if got := pickReadyCredSlot(2, ready); got != 0 {
		t.Fatalf("fallback to slot0: got %d", got)
	}
	ready[2] = true
	if got := pickReadyCredSlot(2, ready); got != 2 {
		t.Fatalf("preferred ready: got %d want 2", got)
	}
	if got := pickReadyCredSlot(1, []bool{false, false, false}); got != -1 {
		t.Fatalf("none ready: got %d want -1", got)
	}
	if got := pickReadyCredSlot(0, []bool{false, true, false}); got != 1 {
		t.Fatalf("any ready: got %d want 1", got)
	}
}

func TestCredPoolSizeForWorkers(t *testing.T) {
	if got := credPoolSizeForWorkers(9); got != 4 {
		t.Fatalf("9 workers → pool %d want 4", got)
	}
}
