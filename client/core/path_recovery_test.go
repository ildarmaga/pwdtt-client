package core

import (
	"context"
	"testing"
	"time"
)

func TestBlockedPathRepairDoesNotBlockWatchdog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan struct{})
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	go func() {
		defer close(done)
		runPathRepairs(ctx, requests, func() {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}()
	select {
	case requests <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("repair worker did not start")
	}
	<-started
	select {
	case requests <- struct{}{}:
		t.Fatal("blocked repair allowed a second request")
	default:
	}
	var p pathRecovery
	now := time.Now()
	if p.check(now.Add(consentTimeout), now) != pathRestart {
		t.Fatal("blocked repair suppressed restart decision")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("repair worker did not stop")
	}
}

func TestPathRecoveryShortOutagePreservesPath(t *testing.T) {
	start := time.Now()
	var p pathRecovery
	if got := p.check(start.Add(15*time.Second), start); got != pathRepair {
		t.Fatalf("first missing reply: %v", got)
	}
	if got := p.check(start.Add(18*time.Second), start); got != pathWaiting {
		t.Fatalf("overlapping repair: %v", got)
	}
	if got := p.check(start.Add(30*time.Second), start); got != pathRepair {
		t.Fatalf("retry on same path: %v", got)
	}
	if got := p.check(start.Add(33*time.Second), start.Add(32*time.Second)); got != pathHealthy || p.recovering {
		t.Fatalf("reply must restore existing path: %v", got)
	}
	if got := p.check(start.Add(48*time.Second), start.Add(32*time.Second)); got != pathRepair {
		t.Fatalf("later outage must get fresh repair: %v", got)
	}
}

func TestPathRecoveryNoRepliesRequiresRestart(t *testing.T) {
	start := time.Now()
	var p pathRecovery
	// Successful outgoing writes do not enter this decision at all.
	for elapsed := 3 * time.Second; elapsed < consentTimeout; elapsed += 3 * time.Second {
		if p.check(start.Add(elapsed), start) == pathRestart {
			t.Fatalf("premature restart at %v", elapsed)
		}
	}
	if got := p.check(start.Add(consentTimeout), start); got != pathRestart {
		t.Fatalf("dead path retained: %v", got)
	}
}
