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
			lastTrafficBytes: 128 * 1024 * 1024,
			probeFails:       wbZombieProbeLimit,
			wantDead:         true,
			wantSoft:         true,
		},
		{
			name:             "probe at limit ignored during meaningful download",
			started:          now.Add(-5 * time.Minute),
			lastHealthy:      now.Add(-time.Second),
			lastTraffic:      now.Add(-5 * time.Second),
			lastFast:         now.Add(-5 * time.Second),
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
			dead, reason, soft := wbTunnelDead(now, c.started, c.lastHealthy, c.lastTraffic, c.lastFast, c.lastTrafficBytes, c.probeFails, c.probeGraceUntil)
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
