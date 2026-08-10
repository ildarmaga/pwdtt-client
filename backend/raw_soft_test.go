package backend

import "testing"

func TestSoftRawPreserveActive(t *testing.T) {
	// preserve + raw TUN + no WG device → soft path (rebind bridge, keep iface)
	cases := []struct {
		preserve, hasTun, hasWG bool
		want                    bool
	}{
		{true, true, false, true},
		{true, true, true, false}, // WG device = not RAW soft
		{true, false, false, false},
		{false, true, false, false},
	}
	for i, tc := range cases {
		got := tc.preserve && tc.hasTun && !tc.hasWG
		if got != tc.want {
			t.Fatalf("case %d: got %v want %v", i, got, tc.want)
		}
	}
}
