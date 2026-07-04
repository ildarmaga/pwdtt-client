package backend

import (
	"testing"
	"time"
)

func TestWBTunnelDead(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name        string
		started     time.Time
		lastHealthy time.Time
		probeFails  int
		wantDead    bool
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
			probeFails:  wbProbeFailLimit - 1,
			wantDead:    false,
		},
		{
			name:        "probe failures at limit kill tunnel even with healthy rtt",
			started:     now.Add(-5 * time.Minute),
			lastHealthy: now.Add(-time.Second), // KCP alive, data path dead
			probeFails:  wbProbeFailLimit,
			wantDead:    true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dead, reason := wbTunnelDead(now, c.started, c.lastHealthy, c.probeFails)
			if dead != c.wantDead {
				t.Fatalf("wbTunnelDead() = %v (%q), want %v", dead, reason, c.wantDead)
			}
			if dead && reason == "" {
				t.Fatalf("dead tunnel must include a reason")
			}
		})
	}
}
