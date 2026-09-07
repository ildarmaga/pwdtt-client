package vkcore

import "testing"

func TestPickReadyCredSlotDoesNotBorrowCredential(t *testing.T) {
	ready := []bool{true, false, true, false}
	if got := pickReadyCredSlot(1, ready); got != -1 {
		t.Fatalf("unready assigned slot must wait, got fallback slot %d", got)
	}
	if got := pickReadyCredSlot(2, ready); got != 2 {
		t.Fatalf("ready assigned slot = %d, want 2", got)
	}
}
