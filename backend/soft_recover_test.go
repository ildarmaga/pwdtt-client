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
		{"first soft", false, 0, true, true, recoverSoft},
		{"second soft still soft", false, 1, true, true, recoverSoft},
		{"after 2 soft → full", false, 2, true, true, recoverFull},
		{"after many soft → full", false, 5, true, true, recoverFull},
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

func TestSoftRecoverSucceededNeedsTraffic(t *testing.T) {
	clearSoftProbeOK()
	defer clearSoftProbeOK()
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)

	// workers alone ≠ OK (18/18 @ 0 B/s bug)
	if softRecoverSucceeded(now, time.Time{}, 0, 9) {
		t.Fatal("workers>0 without traffic must NOT succeed")
	}
	if softRecoverSucceeded(now, time.Time{}, 0, 0) {
		t.Fatal("no workers and no traffic must fail")
	}
	if softRecoverSucceeded(now, now.Add(-time.Second), softRecoverTrafficNeed, 0) {
		t.Fatal("traffic without workers must fail")
	}
	if !softRecoverSucceeded(now, now.Add(-time.Second), softRecoverTrafficNeed, 9) {
		t.Fatal("workers + traffic must succeed")
	}

	MarkSoftProbeOK()
	if !softRecoverSucceeded(now, time.Time{}, 0, 0) {
		t.Fatal("MarkSoftProbeOK must succeed even without bytes yet")
	}
}

func TestSoftRecoverBusy(t *testing.T) {
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	// vkDirect alone must NOT block forever — only the time window does.
	// After workers>0 finishSoft clears until; nested soft on verify-fail must proceed.
	if softRecoverBusy(true, time.Time{}, now) {
		t.Fatal("vkDirect alone must not block nested soft")
	}
	if softRecoverBusy(false, time.Time{}, now) {
		t.Fatal("idle must allow soft")
	}
	if !softRecoverBusy(false, now.Add(30*time.Second), now) {
		t.Fatal("grace window must block")
	}
	if !softRecoverBusy(true, now.Add(30*time.Second), now) {
		t.Fatal("grace window must block even with vkDirect")
	}
	if softRecoverBusy(false, now.Add(-time.Second), now) {
		t.Fatal("expired grace must allow")
	}
	if softRecoverBusy(true, now.Add(-time.Second), now) {
		t.Fatal("expired grace + vkDirect must allow (verify-fail nested soft)")
	}
}

func TestShouldRestoreVKThroughTunnel(t *testing.T) {
	if shouldRestoreVKThroughTunnel(0, true, false) {
		t.Fatal("must stay off tunnel")
	}
	if shouldRestoreVKThroughTunnel(9, true, false) {
		t.Fatal("must stay off tunnel after traffic")
	}
	if shouldRestoreVKThroughTunnel(1, false, true) {
		t.Fatal("must stay off tunnel after probe")
	}
	if shouldRestoreVKThroughTunnel(18, true, true) {
		t.Fatal("must stay off tunnel")
	}
}

func TestSoftAuthBeforeVerify(t *testing.T) {
	if !softAuthBeforeVerify(true, time.Time{}) {
		t.Fatal("vkDirect + no verify = auth in flight")
	}
	if softAuthBeforeVerify(false, time.Time{}) {
		t.Fatal("no vkDirect = not soft auth hold")
	}
	verifyUntil := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	if softAuthBeforeVerify(true, verifyUntil) {
		t.Fatal("verify started = not pre-verify auth hold")
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
	if meaningfulTrafficDelta(12 * 1024) {
		t.Fatal("12KiB/tick (18 workers keepalive) must not count")
	}
	if meaningfulTrafficDelta(softKeepaliveTickMax) {
		t.Fatal("keepalive ceiling must not count")
	}
	if !meaningfulTrafficDelta(softKeepaliveTickMax + 1) {
		t.Fatal("burst above keepalive ceiling must count")
	}
}

func TestFinishSoftRecoverSkipRepeat(t *testing.T) {
	if !finishSoftRecoverShouldSkipRepeat(true, false) {
		t.Fatal("already announced + no new verify → skip")
	}
	if finishSoftRecoverShouldSkipRepeat(false, true) {
		t.Fatal("first announce with verify must run")
	}
	if finishSoftRecoverShouldSkipRepeat(false, false) {
		t.Fatal("first announce without verify must still log once")
	}
	if finishSoftRecoverShouldSkipRepeat(true, true) {
		t.Fatal("new verify despite announced should not skip (shouldStartSoftVerify prevents this)")
	}
}

func TestShouldStartSoftVerifyOnce(t *testing.T) {
	if !shouldStartSoftVerify(true, 1, true, false) {
		t.Fatal("first workers-up after soft must start verify")
	}
	if shouldStartSoftVerify(true, 1, true, true) {
		t.Fatal("already announced must not restart verify")
	}
	if shouldStartSoftVerify(true, 0, true, false) {
		t.Fatal("no soft count → no verify")
	}
	if shouldStartSoftVerify(false, 1, true, false) {
		t.Fatal("VK not in hold → no verify from this path")
	}
	if shouldStartSoftVerify(true, 1, false, false) {
		t.Fatal("verify already running → do not restart")
	}
}

func TestLastTrafficAtForVerifyStartIsZero(t *testing.T) {
	if !lastTrafficAtForVerifyStart().IsZero() {
		t.Fatal("verify start must not stamp lastTrafficAt=now")
	}
}

func TestSimTrafficNoteKeepaliveDoesNotStampZeroClock(t *testing.T) {
	start := time.Date(2026, 8, 17, 13, 17, 0, 0, time.UTC)
	at, bytes, meaningful := SimTrafficNote(time.Time{}, 0, 12*1024, start)
	if meaningful || !at.IsZero() {
		t.Fatal("keepalive must not stamp lastTrafficAt from zero")
	}
	if bytes != 12*1024 {
		t.Fatalf("bytes=%d", bytes)
	}
	at, bytes, meaningful = SimTrafficNote(at, bytes, bytes+12*1024, start.Add(3*time.Second))
	if meaningful || !at.IsZero() {
		t.Fatal("second keepalive tick must keep zero clock")
	}
	burst := bytes + softKeepaliveTickMax + 1
	at, bytes, meaningful = SimTrafficNote(at, bytes, burst, start.Add(6*time.Second))
	if !meaningful || at.IsZero() {
		t.Fatal("real burst must stamp lastTrafficAt")
	}
}

func TestReconnectCooldownSkipsVerifyFail(t *testing.T) {
	now := time.Date(2026, 8, 17, 13, 17, 0, 0, time.UTC)
	last := now.Add(-45 * time.Second)
	if !reconnectCooldownBlocks(false, last, now, autoReconnectCooldown) {
		t.Fatal("stall 45s after soft must still be in cooldown")
	}
	if reconnectCooldownBlocks(true, last, now, autoReconnectCooldown) {
		t.Fatal("verify-fail must skip cooldown so nested soft can run at 45s")
	}
	if reconnectCooldownBlocks(false, last, now.Add(autoReconnectCooldown), autoReconnectCooldown) {
		t.Fatal("after cooldown window stall may reconnect")
	}
}

func TestVKDirectHoldClearsAfterDataPath(t *testing.T) {
	through, hold := vkDirectHoldAfterDataPath(false)
	if through || hold {
		t.Fatal("policy off-tunnel: no restore, hold must end")
	}
	through, hold = vkDirectHoldAfterDataPath(true)
	if !through || hold {
		t.Fatal("if restore allowed: through=true, hold ends")
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
	var lastStart time.Time = start
	for i := 2; i <= softStormMax; i++ {
		ok, c, lastStart = softStormAllows(i-1, lastStart, now.Add(time.Duration(i)*time.Minute))
		if !ok || c != i {
			t.Fatalf("soft #%d: ok=%v count=%d", i, ok, c)
		}
	}
	ok, c, _ = softStormAllows(softStormMax, lastStart, now.Add(6*time.Minute))
	if ok {
		t.Fatalf("over cap must block, count=%d", c)
	}
	ok, c, _ = softStormAllows(softStormMax, lastStart, now.Add(softStormWindow))
	if !ok || c != 1 {
		t.Fatalf("new window must reset: ok=%v count=%d", ok, c)
	}
}

func TestSoftStallImmuneIs90s(t *testing.T) {
	if softStallImmuneAfter != 90*time.Second {
		t.Fatalf("immune want 90s, got %s", softStallImmuneAfter)
	}
}

func TestMarkSoftPreserveConsumed(t *testing.T) {
	SetSoftReconnectPreserve(true)
	markSoftPreserveConsumed()
	if SoftReconnectPreserve() {
		t.Fatal("preserve must clear after consume")
	}
}
