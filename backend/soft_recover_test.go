package backend

import (
	"testing"
	"time"
)

func TestDecideRecoverMode(t *testing.T) {
	cases := []struct {
		name                 string
		forceFull            bool
		softCount            int
		tunnelUp, wgActive   bool
		want                 recoverMode
	}{
		{"force full", true, 0, true, true, recoverFull},
		{"soft first", false, 0, true, true, recoverSoft},
		{"soft exhausted", false, 1, true, true, recoverFull},
		{"no wg", false, 0, true, false, recoverFull},
		{"tunnel down", false, 0, false, true, recoverFull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideRecoverMode(tc.forceFull, tc.softCount, tc.tunnelUp, tc.wgActive)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSoftRecoverTrafficOK(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	okAt := now.Add(-2 * time.Second)
	staleAt := now.Add(-trafficStallThreshold - time.Second)

	if softRecoverTrafficOK(now, time.Time{}, softRecoverTrafficNeed) {
		t.Fatal("zero lastTrafficAt must fail")
	}
	if softRecoverTrafficOK(now, staleAt, softRecoverTrafficNeed) {
		t.Fatal("stale traffic must fail")
	}
	if softRecoverTrafficOK(now, okAt, softRecoverTrafficNeed-1) {
		t.Fatal("too few bytes must fail")
	}
	if !softRecoverTrafficOK(now, okAt, softRecoverTrafficNeed) {
		t.Fatal("fresh + enough bytes must pass")
	}
}
