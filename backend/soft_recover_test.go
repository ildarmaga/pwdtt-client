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

func TestMeaningfulTrafficDelta(t *testing.T) {
	if meaningfulTrafficDelta(1024) {
		t.Fatal("1KiB keepalive must not count")
	}
	if meaningfulTrafficDelta(trafficStallMinBytes - 1) {
		t.Fatal("just below threshold must not count")
	}
	if !meaningfulTrafficDelta(trafficStallMinBytes) {
		t.Fatal("threshold must count")
	}
}

func TestShouldStallSoft(t *testing.T) {
	if shouldStallSoft(false, 30*time.Second, time.Minute, true, false, false) {
		t.Fatal("no watch → no soft")
	}
	if shouldStallSoft(true, 2*time.Second, time.Minute, true, false, false) {
		t.Fatal("short stall → no soft")
	}
	if !shouldStallSoft(true, trafficStallThreshold, time.Minute, true, false, false) {
		t.Fatal("was active + stall → soft")
	}
	if shouldStallSoft(true, trafficStallThreshold, 5*time.Second, false, false, false) {
		t.Fatal("startup grace → no soft yet")
	}
	if !shouldStallSoft(true, trafficStallThreshold, trafficStallStartupGrace, false, false, false) {
		t.Fatal("past startup + no traffic → soft")
	}
	if shouldStallSoft(true, trafficStallThreshold, time.Minute, true, true, false) {
		t.Fatal("post-soft immune → no stall soft")
	}
	if shouldStallSoft(true, trafficStallThreshold, time.Minute, true, false, true) {
		t.Fatal("verify window → no stall soft")
	}
}

func TestFinishSoftRecoverSkipsDyingSession(t *testing.T) {
	// softSwap без Start — старые воркеры не должны считаться «готовыми».
	if !shouldSkipFinishSoftRecover(true, false) {
		t.Fatal("dying old session must skip finish")
	}
	if shouldSkipFinishSoftRecover(true, true) {
		t.Fatal("after Start finish allowed")
	}
	if shouldSkipFinishSoftRecover(false, false) {
		t.Fatal("idle session finish allowed")
	}
}

func TestSoftPreserveUntilConfigApply(t *testing.T) {
	if shouldClearSoftPreserveOnWorkerReady() {
		t.Fatal("READY must not clear preserve (TunAlreadyReady race)")
	}
	if !decideSoftApplyPath(true, true) {
		t.Fatal("preserve+TUN → soft apply")
	}
	if decideSoftApplyPath(true, false) {
		t.Fatal("preserve without TUN → full create")
	}
	if decideSoftApplyPath(false, true) {
		t.Fatal("no preserve → full create")
	}
}

// Матрица: когда stall-soft имеет право рвать сессию (без живого VPN).
func TestStallSoftDecisionMatrix(t *testing.T) {
	thr := trafficStallThreshold
	cases := []struct {
		name                         string
		watch                        bool
		stall, since                 time.Duration
		wasActive, immune, verifying bool
		want                         bool
	}{
		{"zombie keepalive после активности", true, thr, time.Minute, true, false, false, true},
		{"idle 8s после soft — НЕ soft (старый баг 283)", true, 8 * time.Second, time.Minute, true, false, false, false},
		{"idle 20s после soft immune", true, thr, time.Minute, true, true, false, false},
		{"во время verify окна", true, thr, time.Minute, true, false, true, false},
		{"старт без трафика < grace", true, thr, 5 * time.Second, false, false, false, false},
		{"старт без трафика > grace", true, thr, trafficStallStartupGrace, false, false, false, true},
		{"keepalive delta не двигает stall — уже в stallDur", true, thr, time.Minute, true, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldStallSoft(tc.watch, tc.stall, tc.since, tc.wasActive, tc.immune, tc.verifying)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestMarkSoftPreserveConsumed(t *testing.T) {
	SetSoftReconnectPreserve(true)
	if !SoftReconnectPreserve() {
		t.Fatal("preserve must be on")
	}
	markSoftPreserveConsumed()
	if SoftReconnectPreserve() {
		t.Fatal("preserve must clear after consume")
	}
	markSoftPreserveConsumed() // idempotent
	if SoftReconnectPreserve() {
		t.Fatal("still off")
	}
}
