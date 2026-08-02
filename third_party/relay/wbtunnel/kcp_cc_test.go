package wbtunnel

import "testing"

// TestNextKCPWnd locks the delay-based AIMD window controller: additive-increase
// while RTT sits near the floor (path free), multiplicative-decrease once RTT
// inflates past the floor (queue building), clamped to [min,cap].
func TestNextKCPWnd(t *testing.T) {
	// No RTT data yet → window unchanged.
	if got, _ := nextKCPWnd(200, 0, 0, 0, 0); got != 200 {
		t.Fatalf("no data should leave window: got %d", got)
	}
	// Near floor (fast 200 < 200*1.15+15=245) → grow by step.
	if got, _ := nextKCPWnd(200, 200, 200, 200, 0); got != 200+kcpWndGrowStep {
		t.Fatalf("near floor should grow: got %d want %d", got, 200+kcpWndGrowStep)
	}
	// Inflated past shrink threshold (400 > 200*1.30+15=275) with the streak already
	// primed → hard proportional shrink by factor = floor/ewma = 200/400 = 0.5.
	if got, _ := nextKCPWnd(400, 400, 400, 200, kcpShrinkStreak-1); got != int(float64(400)*0.5) {
		t.Fatalf("sustained inflated RTT should shrink proportionally: got %d want %d", got, int(float64(400)*0.5))
	}
	// Severe bufferbloat (ewma ≫ floor), streak primed → factor clamps to hardest cut.
	if got, _ := nextKCPWnd(1024, 3000, 3000, 100, kcpShrinkStreak-1); got != int(float64(1024)*kcpWndShrinkFloor) {
		t.Fatalf("severe bloat should clamp to hardest cut: got %d want %d", got, int(float64(1024)*kcpWndShrinkFloor))
	}
	// In the dead-band between grow and shrink thresholds → hold steady.
	// floor=200: grow<245, shrink>275; ewma=260 and fast=260 are in-between.
	if got, streak := nextKCPWnd(300, 260, 260, 200, 1); got != 300 || streak != 0 {
		t.Fatalf("dead-band should hold window and reset streak: got %d streak %d", got, streak)
	}
	// Shrink never goes below the floor window.
	if got, _ := nextKCPWnd(kcpSndWndMin, 5000, 5000, 100, kcpShrinkStreak); got != kcpSndWndMin {
		t.Fatalf("shrink must clamp to min: got %d", got)
	}
	// Grow never exceeds the cap.
	if got, _ := nextKCPWnd(kcpSndWndCap, 10, 10, 10, 0); got != kcpSndWndCap {
		t.Fatalf("grow must clamp to cap: got %d", got)
	}
}

// TestNextKCPWndFastRecovery is the regression for the field-observed throttle:
// after an upload-burst RTT spike the slow smoothed ewma stays elevated for many
// ticks, but the path has actually cleared (recent-min RTT back near the floor).
// The window MUST grow back on the fast signal instead of sitting pinned at the
// floor (field: rtt=44ms but wnd=64 for 90s → download sawtooth). Grow is gated
// on rttFast, not rttEwma.
func TestNextKCPWndFastRecovery(t *testing.T) {
	const floor = 50.0
	// ewma still elevated at 240ms (between grow 72.5 and shrink 80 — actually above
	// shrink), but the recent-min RTT has returned to the floor. Use an ewma that is
	// in the dead-band for the slow signal yet would previously block grow.
	// floor=50: grow<72.5, shrink>80. ewma=78 is dead-band (no shrink), fast=48<72.5.
	got, streak := nextKCPWnd(64, 78, 48, floor, 0)
	if got != 64+kcpWndGrowStep {
		t.Fatalf("fast recovery should grow the window off the floor: got %d want %d", got, 64+kcpWndGrowStep)
	}
	if streak != 0 {
		t.Fatalf("recovery should reset the shrink streak: got %d", streak)
	}
	// Guard: a dead-band ewma (still slightly elevated from a decaying spike, not
	// congested) must NOT block grow when the recent-min RTT is at the floor —
	// this is exactly the "stuck at wnd=64" state. Several such ticks must climb.
	wnd := kcpSndWndMin
	for i := 0; i < 4; i++ {
		wnd, _ = nextKCPWnd(wnd, 78, 48, floor, 0) // ewma dead-band, fast at floor
	}
	if wnd <= kcpSndWndMin {
		t.Fatalf("window must climb off the floor once the path clears: got %d", wnd)
	}
}

// TestNextKCPWndGrowBeatsElevatedEwma is the 0.3.200 field case: floor=155,
// ewma=250 (above shrinkThresh=216.5) while recent-min RTT is already 185
// (below growThresh=193). Grow MUST win — otherwise wnd stays at 64 forever.
func TestNextKCPWndGrowBeatsElevatedEwma(t *testing.T) {
	const floor = 155.0
	got, streak := nextKCPWnd(64, 250, 185, floor, 2)
	if got != 64+kcpWndGrowStep {
		t.Fatalf("elevated ewma must not block grow when fast RTT is clear: got %d want %d", got, 64+kcpWndGrowStep)
	}
	if streak != 0 {
		t.Fatalf("grow must reset shrink streak: got %d", streak)
	}
	// Sustained congestion (fast also high) still shrinks.
	hard, s := nextKCPWnd(200, 250, 250, floor, kcpShrinkStreak-1)
	if s < kcpShrinkStreak {
		t.Fatalf("sustained high fast+ewma should keep shrinking: streak=%d", s)
	}
	if hard >= 200 {
		t.Fatalf("sustained congestion must cut window: got %d", hard)
	}
}

// TestNextKCPWndHysteresis locks the anti-sawtooth behaviour: a single transient
// RTT spike (lossy-TURN jitter) must NOT collapse a fast window — only a gentle
// trim — and the window recovers once RTT returns to the floor. Only RTT that
// stays high for kcpShrinkStreak consecutive ticks triggers the hard cut.
func TestNextKCPWndHysteresis(t *testing.T) {
	const floor = 100.0
	const spike = 400.0 // > 100*1.30+15=145 → "high"
	const base = 300    // below kcpSndWndCap so the trim math isn't clamped

	// One-tick spike from a fresh streak → gentle trim, streak=1.
	got, streak := nextKCPWnd(base, spike, spike, floor, 0)
	if streak != 1 {
		t.Fatalf("first high tick should set streak=1: got %d", streak)
	}
	if want := int(base * kcpWndSoftShrink); got != want {
		t.Fatalf("transient spike must trim gently, not hard-cut: got %d want %d", got, want)
	}

	// RTT returns to the floor → grow and reset streak (window recovers, no sawtooth).
	rec, streak := nextKCPWnd(got, floor, floor, floor, streak)
	if streak != 0 || rec != got+kcpWndGrowStep {
		t.Fatalf("recovery should grow and reset streak: got %d streak %d", rec, streak)
	}

	// Sustained high RTT: second consecutive high tick → hard proportional cut.
	_, s1 := nextKCPWnd(base, spike, spike, floor, 0)
	hard, s2 := nextKCPWnd(base, spike, spike, floor, s1)
	if s2 < kcpShrinkStreak {
		t.Fatalf("sustained high RTT should reach the shrink streak: got %d", s2)
	}
	factor := floor / spike
	if factor < kcpWndShrinkFloor {
		factor = kcpWndShrinkFloor
	}
	if want := int(base * factor); hard != want && hard != kcpSndWndMin {
		// Clamped to floor when proportional cut would go below kcpSndWndMin.
		if hard < want {
			t.Fatalf("sustained bloat should hard-cut: got %d want %d (or min %d)", hard, want, kcpSndWndMin)
		}
	}
	if hard > base {
		t.Fatalf("sustained bloat must shrink: got %d base %d", hard, base)
	}
}

// TestNextKCPWndDrainsBufferbloat simulates the observed failure: the window is
// sized high for a fast download (≈cap) when a sudden upload burst inflates RTT
// (field log: floor 66ms → 800ms+). The proportional cut must collapse the window
// to the minimum within a couple of ticks so ~1.2 MB of in-flight drains and the
// carrier RTT recovers, instead of the old fixed 0.7/tick that took ~8 ticks.
func TestNextKCPWndDrainsBufferbloat(t *testing.T) {
	floor := 66.0
	wnd := kcpSndWndCap
	// RTT inflated to 800ms (as in the field log). Track how fast we reach min.
	ticks := 0
	prev := wnd
	streak := 0
	for wnd > kcpSndWndMin && ticks < 10 {
		wnd, streak = nextKCPWnd(wnd, 800, 800, floor, streak)
		if wnd >= prev {
			t.Fatalf("tick %d: window did not shrink under bufferbloat: %d >= %d", ticks, wnd, prev)
		}
		prev = wnd
		ticks++
	}
	if wnd != kcpSndWndMin {
		t.Fatalf("bufferbloat should drain window to min: got %d", wnd)
	}
	// One soft trim then hard 0.25 cuts: cap→min in ~3 ticks (1024→921→230→64).
	// Still fast enough to drain a genuinely bloated carrier before it runs away.
	if ticks > 4 {
		t.Fatalf("window drained too slowly (%d ticks) — carrier would stay bloated", ticks)
	}
}

// TestUpdateRTTFloor verifies the floor snaps down instantly to a new minimum but
// only creeps up slowly, so transient bufferbloat can't drag the baseline up (which
// would hide congestion and stop the window from shrinking).
func TestUpdateRTTFloor(t *testing.T) {
	if got := updateRTTFloor(0, 50); got != 50 {
		t.Fatalf("first sample should seed floor: got %.2f", got)
	}
	if got := updateRTTFloor(50, 40); got != 40 {
		t.Fatalf("lower RTT must snap the floor down: got %.2f", got)
	}
	// A mildly-high sample within the non-congested band creeps the floor up only
	// slightly: floor=100, ewma=120 (<100*1.30+15=145) → 100 + (120-100)*0.01 = 100.2.
	up := updateRTTFloor(100, 120)
	if up <= 100 || up > 101 {
		t.Fatalf("in-band sample should creep floor up only slightly: got %.2f", up)
	}
	// Zero/invalid RTT leaves the floor untouched.
	if got := updateRTTFloor(40, 0); got != 40 {
		t.Fatalf("invalid RTT must not change floor: got %.2f", got)
	}
	// While congested (RTT past the shrink threshold) the floor is FROZEN — not
	// even a slow creep — so the shrink threshold stays low and the window keeps
	// shrinking. floor=200, ewma=3000 (≫ 200*1.30+15) → floor unchanged.
	if got := updateRTTFloor(200, 3000); got != 200 {
		t.Fatalf("congested sample must freeze the floor: got %.2f", got)
	}
	// Even 60s of bufferbloat (3000ms) leaves a 200ms floor exactly where it was.
	floor := 200.0
	for i := 0; i < 60; i++ {
		floor = updateRTTFloor(floor, 3000)
	}
	if floor != 200 {
		t.Fatalf("floor must not creep at all under bufferbloat: got %.2f", floor)
	}
}
