package wbstream

import "testing"

func TestShouldDrainRemoteVP8(t *testing.T) {
	cases := []struct {
		wantDual bool
		count    int
		drain    bool
	}{
		{false, 1, false},
		{false, 2, true},
		{false, 3, true},
		{true, 1, false},
		{true, 2, false},
		{true, 3, true}, // ICE renegotiation track #3 — field bug
		{true, 4, true},
	}
	for _, tc := range cases {
		got := shouldDrainRemoteVP8(tc.wantDual, tc.count)
		if got != tc.drain {
			t.Fatalf("wantDual=%v count=%d: drain=%v want %v",
				tc.wantDual, tc.count, got, tc.drain)
		}
	}
}
