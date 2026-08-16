package backend

import (
	"testing"
	"time"
)

func TestWBTunnelDead(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name             string
		started          time.Time
		lastHealthy      time.Time
		lastTraffic      time.Time
		lastFast         time.Time
		lastRxAt         time.Time
		lastTrafficBytes int64
		probeFails       int
		probeGraceUntil  time.Time
		wantDead         bool
		wantSoft         bool
	}{
		{
			name:        "healthy rtt just now",
			started:     now.Add(-5 * time.Minute),
			lastHealthy: now.Add(-3 * time.Second),
			wantDead:    false,
		},
		{
			name:        "rtt stale beyond dead timeout",
			started:     now.Add(-5 * time.Minute),
			lastHealthy: now.Add(-wbDeadTimeout - time.Second),
			wantDead:    true,
			wantSoft:    true,
		},
		{
			name:     "connecting, still within connect timeout",
			started:  now.Add(-30 * time.Second),
			wantDead: false,
		},
		{
			name:     "never became healthy",
			started:  now.Add(-wbConnectTimeout - time.Second),
			wantDead: true,
		},
		{
			name:        "probe failures below limit keep tunnel",
			started:     now.Add(-5 * time.Minute),
			lastHealthy: now.Add(-time.Second),
			probeFails:  wbZombieProbeLimit - 1,
			wantDead:    false,
		},
		{
			name:             "zombie: probe at limit with trickle only",
			started:          now.Add(-5 * time.Minute),
			lastHealthy:      now.Add(-time.Second),
			lastTraffic:      now.Add(-5 * time.Second),
			lastRxAt:         now.Add(-wbDownloadStallWindow - time.Second),
			lastTrafficBytes: 128 * 1024 * 1024,
			probeFails:       wbZombieProbeLimit,
			wantDead:         true,
			wantSoft:         true,
		},
		{
			name:             "upload trickle but download still moving — keep tunnel",
			started:          now.Add(-5 * time.Minute),
			lastHealthy:      now.Add(-time.Second),
			lastTraffic:      now.Add(-5 * time.Second),
			lastRxAt:         now.Add(-5 * time.Second),
			lastTrafficBytes: 128 * 1024 * 1024,
			probeFails:       wbZombieProbeLimit,
			wantDead:         false,
		},
		{
			name:             "probe at limit ignored during meaningful download",
			started:          now.Add(-5 * time.Minute),
			lastHealthy:      now.Add(-time.Second),
			lastTraffic:      now.Add(-5 * time.Second),
			lastFast:         now.Add(-5 * time.Second),
			lastRxAt:         now.Add(-5 * time.Second),
			lastTrafficBytes: 50 * 1024 * 1024,
			probeFails:       wbProbeFailLimit,
			wantDead:         false,
		},
		{
			name:            "probe skipped during rebind grace",
			started:         now.Add(-5 * time.Minute),
			lastHealthy:     now.Add(-time.Second),
			lastTraffic:     now.Add(-wbTrafficStallWindow - time.Second),
			probeFails:      wbZombieProbeLimit,
			probeGraceUntil: now.Add(30 * time.Second),
			wantDead:        false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dead, reason, soft := wbTunnelDead(now, c.started, c.lastHealthy, c.lastTraffic, c.lastFast, c.lastRxAt, c.lastTrafficBytes, c.probeFails, c.probeGraceUntil)
			if dead != c.wantDead {
				t.Fatalf("wbTunnelDead() dead = %v (%q), want %v", dead, reason, c.wantDead)
			}
			if soft != c.wantSoft {
				t.Fatalf("wbTunnelDead() soft = %v, want %v", soft, c.wantSoft)
			}
			if dead && reason == "" {
				t.Fatalf("dead tunnel must include a reason")
			}
		})
	}
}

func TestWBTrafficMeaningful(t *testing.T) {
	now := time.Now()
	if !wbTrafficMeaningful(now, now.Add(-5*time.Second)) {
		t.Fatal("expected meaningful traffic")
	}
	if wbTrafficMeaningful(now, now.Add(-wbMeaningfulWindow-time.Second)) {
		t.Fatal("expected not meaningful after window")
	}
}

func TestWBTrafficActive(t *testing.T) {
	now := time.Now()
	if !wbTrafficActive(now, now.Add(-10*time.Second), 1024*1024) {
		t.Fatal("expected active traffic")
	}
}

func TestWBSocksIgnoreDeadRTT(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name        string
		reason      string
		lastTraffic time.Time
		lastBytes   int64
		wantIgnore  bool
	}{
		{
			name:        "rtt dead but SOCKS bytes still move — keep session",
			reason:      "нет живого RTT",
			lastTraffic: now.Add(-5 * time.Second),
			lastBytes:   1024 * 1024,
			wantIgnore:  true,
		},
		{
			name:        "rtt dead and traffic stalled — must reconnect",
			reason:      "нет живого RTT",
			lastTraffic: now.Add(-wbTrafficStallWindow - time.Second),
			lastBytes:   1024 * 1024,
			wantIgnore:  false,
		},
		{
			name:       "rtt dead and never any traffic — must reconnect",
			reason:     "нет живого RTT",
			wantIgnore: false,
		},
		{
			name:        "other dead reason is not an RTT skip",
			reason:      "туннель завис (zombie)",
			lastTraffic: now.Add(-wbTrafficStallWindow - time.Second),
			lastBytes:   1024 * 1024,
			wantIgnore:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := wbSocksIgnoreDeadRTT(c.reason, now, c.lastTraffic, c.lastBytes)
			if got != c.wantIgnore {
				t.Fatalf("wbSocksIgnoreDeadRTT() = %v, want %v", got, c.wantIgnore)
			}
		})
	}
}

// A shutdown watcher for run N must not clear (or emergency-stop) run N+1:
// the user can reconnect while the old run is still tearing down.
func TestAwaitShutdownRunSupersededGeneration(t *testing.T) {
	m := &WBManager{}
	oldGen := m.runGen.Add(1)
	oldDone := make(chan struct{}) // old run never finishes in time

	// A new run took over.
	m.runGen.Add(1)
	newCancel := func() {}
	m.mu.Lock()
	m.cancel = newCancel
	m.done = make(chan struct{})
	m.mu.Unlock()

	finished := make(chan struct{})
	go func() {
		m.awaitShutdownRun(oldDone, oldGen, 10*time.Millisecond)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("awaitShutdownRun did not return after deadline")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel == nil || m.done == nil {
		t.Fatal("superseded watcher must not clear the new run's state")
	}
}

func TestAwaitShutdownRunClearsOwnGeneration(t *testing.T) {
	m := &WBManager{}
	gen := m.runGen.Add(1)
	done := make(chan struct{})
	m.mu.Lock()
	m.cancel = func() {}
	m.done = done
	m.mu.Unlock()

	close(done) // run exits cleanly
	m.awaitShutdownRun(done, gen, time.Second)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil || m.done != nil {
		t.Fatal("watcher must clear state of its own finished run")
	}
}

func TestClearRunStaleGeneration(t *testing.T) {
	m := &WBManager{}
	oldGen := m.runGen.Add(1)
	m.runGen.Add(1)
	m.mu.Lock()
	m.cancel = func() {}
	m.mu.Unlock()

	m.clearRun(oldGen)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel == nil {
		t.Fatal("clearRun with stale generation must be a no-op")
	}
}
