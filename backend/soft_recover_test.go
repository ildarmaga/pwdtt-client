package backend

import (
	"testing"
	"time"
)

func TestDecideRecoverMode(t *testing.T) {
	cases := []struct {
		name               string
		forceFull          bool
		softCount          int
		tunnelUp, wgActive bool
		want               recoverMode
	}{
		{"force full (сеть)", true, 0, true, true, recoverFull},
		{"soft always", false, 0, true, true, recoverSoft},
		{"soft даже после многих soft", false, 5, true, true, recoverSoft},
		{"no wg → full", false, 0, true, false, recoverFull},
		{"tunnel down → full", false, 0, false, true, recoverFull},
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

func TestSoftRecoverBusy(t *testing.T) {
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	if !softRecoverBusy(true, time.Time{}, now) {
		t.Fatal("vkDirect must block nested soft")
	}
	if softRecoverBusy(false, time.Time{}, now) {
		t.Fatal("idle must allow soft")
	}
	if !softRecoverBusy(false, now.Add(30*time.Second), now) {
		t.Fatal("grace window must block")
	}
	if softRecoverBusy(false, now.Add(-time.Second), now) {
		t.Fatal("expired grace must allow")
	}
}

func TestShouldAutoSoftOnCoreEnd(t *testing.T) {
	if !shouldAutoSoftOnCoreEnd(false, false, false, true, true) {
		t.Fatal("natural CORE death with TUN must auto-soft")
	}
	if shouldAutoSoftOnCoreEnd(true, false, false, true, true) {
		t.Fatal("preserve soft-swap must not double-soft")
	}
	if shouldAutoSoftOnCoreEnd(false, true, false, true, true) {
		t.Fatal("softSwapInProgress must not auto-soft")
	}
	if shouldAutoSoftOnCoreEnd(false, false, true, true, true) {
		t.Fatal("explicit Stop (suppress) must not auto-soft")
	}
	if shouldAutoSoftOnCoreEnd(false, false, false, false, true) {
		t.Fatal("tunnel already down must not auto-soft")
	}
	if shouldAutoSoftOnCoreEnd(false, false, false, true, false) {
		t.Fatal("no TUN iface must not auto-soft")
	}
}
