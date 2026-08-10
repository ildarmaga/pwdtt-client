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

func TestSoftRecoverSucceededIdleWithWorkers(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	// Раньше idle после soft → «Soft без трафика» → шторм. Теперь workers>0 = OK.
	if !softRecoverSucceeded(now, time.Time{}, 0, 9) {
		t.Fatal("workers>0 must succeed even without traffic")
	}
	if softRecoverSucceeded(now, time.Time{}, 0, 0) {
		t.Fatal("no workers and no traffic must fail")
	}
	if !softRecoverSucceeded(now, now.Add(-time.Second), softRecoverTrafficNeed, 0) {
		t.Fatal("traffic alone must still succeed")
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
	thr := trafficStallThreshold
	if shouldStallSoft(false, 30*time.Second, time.Minute, true, false, false, 18) {
		t.Fatal("no watch → no soft")
	}
	if shouldStallSoft(true, 2*time.Second, time.Minute, true, false, false, 18) {
		t.Fatal("short stall → no soft")
	}
	if !shouldStallSoft(true, thr, time.Minute, true, false, false, 18) {
		t.Fatal("zombie workers+stall → soft")
	}
	if shouldStallSoft(true, thr, time.Minute, true, false, false, 0) {
		t.Fatal("workers=0 → workersLost path, not stall")
	}
	if shouldStallSoft(true, thr, 5*time.Second, false, false, false, 18) {
		t.Fatal("startup grace → no soft yet")
	}
	if !shouldStallSoft(true, thr, trafficStallStartupGrace, false, false, false, 18) {
		t.Fatal("past startup + no traffic → soft")
	}
	if shouldStallSoft(true, thr, time.Minute, true, true, false, 18) {
		t.Fatal("post-soft immune → no stall soft")
	}
	if shouldStallSoft(true, thr, time.Minute, true, false, true, 18) {
		t.Fatal("verify window → no stall soft")
	}
	// Idle 20–40s after burst must NOT soft (старый баг шторма).
	if shouldStallSoft(true, 40*time.Second, time.Minute, true, false, false, 18) {
		t.Fatal("40s idle with workers must not soft (need 3m)")
	}
	if shouldStallSoft(true, 2*time.Minute, time.Minute, true, false, false, 18) {
		t.Fatal("2m idle must not soft yet")
	}
}

func TestFinishSoftRecoverSkipsDyingSession(t *testing.T) {
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
		t.Fatal("READY must not clear preserve")
	}
	if !decideSoftApplyPath(true, true) {
		t.Fatal("preserve+TUN → soft apply")
	}
	if decideSoftApplyPath(true, false) {
		t.Fatal("preserve without TUN → full create")
	}
}

func TestSoftStormAllows(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	ok, c, start := softStormAllows(0, time.Time{}, now)
	if !ok || c != 1 {
		t.Fatalf("first soft: ok=%v count=%d", ok, c)
	}
	ok, c, start = softStormAllows(1, start, now.Add(time.Minute))
	if !ok || c != 2 {
		t.Fatalf("second: ok=%v count=%d", ok, c)
	}
	ok, c, start = softStormAllows(2, start, now.Add(2*time.Minute))
	if !ok || c != 3 {
		t.Fatalf("third: ok=%v count=%d", ok, c)
	}
	ok, c, _ = softStormAllows(3, start, now.Add(3*time.Minute))
	if ok {
		t.Fatalf("fourth in window must block, count=%d", c)
	}
	ok, c, _ = softStormAllows(3, start, now.Add(softStormWindow))
	if !ok || c != 1 {
		t.Fatalf("new window must reset: ok=%v count=%d", ok, c)
	}
}

func TestMarkSoftPreserveConsumed(t *testing.T) {
	SetSoftReconnectPreserve(true)
	markSoftPreserveConsumed()
	if SoftReconnectPreserve() {
		t.Fatal("preserve must clear after consume")
	}
}
