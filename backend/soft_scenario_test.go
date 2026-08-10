package backend

import (
	"fmt"
	"testing"
	"time"
)

// Симулятор политики soft: шаги по времени без живого VPN/сервера.
type softSim struct {
	t              *testing.T
	now            time.Time
	connectedAt    time.Time
	lastTrafficAt  time.Time
	lastBytes      int64
	bytesAtSoft    int64
	workers        int32
	wasActive      bool
	immuneUntil    time.Time
	verifyUntil    time.Time
	stormCount     int
	stormStarted   time.Time
	softBusyUntil  time.Time
	cooldownUntil  time.Time
	softEvents     []string
}

func newSoftSim(t *testing.T, start time.Time) *softSim {
	t.Helper()
	return &softSim{
		t:             t,
		now:           start,
		connectedAt:   start,
		lastTrafficAt: start,
		workers:       18,
		wasActive:     true,
	}
}

func (s *softSim) advance(d time.Duration) {
	s.now = s.now.Add(d)
}

func (s *softSim) addBytes(n int64) {
	total := s.lastBytes + n
	at, bytes, meaningful := SimTrafficNote(s.lastTrafficAt, s.lastBytes, total, s.now)
	s.lastTrafficAt = at
	s.lastBytes = bytes
	if meaningful {
		s.wasActive = true
	}
}

func (s *softSim) keepaliveTick() {
	// ~1 KiB keepalive за тик — не должен двигать lastTrafficAt.
	s.addBytes(1024)
}

func (s *softSim) realTraffic(n int64) {
	s.addBytes(n)
}

func (s *softSim) beginSoft() {
	s.softEvents = append(s.softEvents, fmt.Sprintf("soft@%s", s.now.Format("15:04:05")))
	s.softBusyUntil = s.now.Add(softRecoverAuthGrace)
	s.immuneUntil = s.now.Add(softStallImmuneAfter)
	s.verifyUntil = s.now.Add(softRecoverVerifyWait)
	s.bytesAtSoft = s.lastBytes
	s.cooldownUntil = s.now.Add(autoReconnectCooldown)
	s.lastBytes = 0
	s.lastTrafficAt = s.now
	s.workers = 0
}

func (s *softSim) workersUp(n int32) {
	s.workers = n
	if n > 0 {
		s.verifyUntil = time.Time{} // как noteWorkerStats: verify OK сразу
		s.softBusyUntil = time.Time{}
	}
}

func (s *softSim) tick() SoftTickResult {
	stallDur := time.Duration(0)
	if !s.lastTrafficAt.IsZero() {
		stallDur = s.now.Sub(s.lastTrafficAt)
	}
	in := SoftTickInput{
		Now:            s.now,
		Watch:          true,
		StallDur:       stallDur,
		SinceConnect:   s.now.Sub(s.connectedAt),
		WasActive:      s.wasActive,
		StallImmune:    !s.immuneUntil.IsZero() && s.now.Before(s.immuneUntil),
		VerifyPending:  !s.verifyUntil.IsZero() && s.now.Before(s.verifyUntil),
		VerifyExpired:  !s.verifyUntil.IsZero() && !s.now.Before(s.verifyUntil),
		Workers:        s.workers,
		LastTrafficAt:  s.lastTrafficAt,
		BytesSinceSoft: s.lastBytes - s.bytesAtSoft,
		SoftBusy:       !s.softBusyUntil.IsZero() && s.now.Before(s.softBusyUntil),
		CooldownActive:  !s.cooldownUntil.IsZero() && s.now.Before(s.cooldownUntil),
		StormCount:     s.stormCount,
		StormStarted:   s.stormStarted,
	}
	res := DecideSoftTick(in)
	switch res.Action {
	case softTickStallSoft, softTickVerifyFailSoft:
		s.stormCount = res.StormCount
		s.stormStarted = res.StormStarted
		s.beginSoft()
	case softTickStormBlock:
		s.softEvents = append(s.softEvents, fmt.Sprintf("storm_block@%s", s.now.Format("15:04:05")))
	case softTickVerifyOK:
		s.verifyUntil = time.Time{}
	}
	return res
}

func (s *softSim) softCount() int { return len(s.softEvents) }

func TestScenario_IdleBrowsingNoSoftStorm(t *testing.T) {
	// Реальный баг: burst → idle 20–120с → soft → soft. 2 мин idle — ещё не zombie.
	s := newSoftSim(t, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	s.realTraffic(5 << 20) // 5 MiB burst
	for i := 0; i < 60; i++ { // 2 минуты по 2с — ниже stall threshold 3м
		s.advance(2 * time.Second)
		s.keepaliveTick()
		res := s.tick()
		if res.Action == softTickStallSoft || res.Action == softTickVerifyFailSoft {
			t.Fatalf("idle keepalive must not soft at t+%s action=%d events=%v",
				s.now.Sub(s.connectedAt), res.Action, s.softEvents)
		}
	}
	if s.softCount() != 0 {
		t.Fatalf("want 0 softs, got %d %v", s.softCount(), s.softEvents)
	}
}

func TestScenario_Zombie18WorkersTriggersSoft(t *testing.T) {
	s := newSoftSim(t, time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC))
	s.realTraffic(10 << 20)
	// 3м+ только keepalive → zombie soft.
	for i := 0; i < 100; i++ {
		s.advance(2 * time.Second)
		s.keepaliveTick()
		res := s.tick()
		if res.Action == softTickStallSoft {
			s.workersUp(18)
			if s.softCount() != 1 {
				t.Fatalf("want 1 soft, got %d", s.softCount())
			}
			return
		}
	}
	t.Fatalf("expected stall soft after ~3m zombie, events=%v stall=%s",
		s.softEvents, s.now.Sub(s.lastTrafficAt))
}

func TestScenario_AfterSoftIdleVerifyOK(t *testing.T) {
	// Раньше: soft → workers up → idle 30с → verify fail → soft снова.
	s := newSoftSim(t, time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC))
	s.realTraffic(1 << 20)
	// Форсируем soft вручную как после zombie.
	s.beginSoft()
	s.stormCount = 1
	s.stormStarted = s.now
	s.advance(5 * time.Second)
	s.workersUp(18)
	// 2 минуты idle+keepalive — не должно быть второго soft.
	for i := 0; i < 60; i++ {
		s.advance(2 * time.Second)
		s.keepaliveTick()
		res := s.tick()
		if res.Action == softTickStallSoft || res.Action == softTickVerifyFailSoft {
			t.Fatalf("post-soft idle must not re-soft: %v action=%d", s.softEvents, res.Action)
		}
		if res.Action == softTickVerifyOK {
			// ok, cleared
		}
	}
	if s.softCount() != 1 {
		t.Fatalf("want exactly 1 soft, got %d %v", s.softCount(), s.softEvents)
	}
}

func TestScenario_SoftStormCap(t *testing.T) {
	s := newSoftSim(t, time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC))
	s.immuneUntil = time.Time{} // без immune
	s.cooldownUntil = time.Time{}
	for round := 0; round < 5; round++ {
		s.lastTrafficAt = s.now.Add(-trafficStallThreshold - time.Second)
		s.workers = 18
		s.wasActive = true
		s.softBusyUntil = time.Time{}
		s.cooldownUntil = time.Time{}
		s.immuneUntil = time.Time{}
		s.verifyUntil = time.Time{}
		res := s.tick()
		if round < softStormMax {
			if res.Action != softTickStallSoft {
				t.Fatalf("round %d want stall soft, got %d", round, res.Action)
			}
			s.workersUp(18)
			s.advance(autoReconnectCooldown + time.Second)
			continue
		}
		if res.Action != softTickStormBlock {
			t.Fatalf("round %d want storm block, got %d events=%v", round, res.Action, s.softEvents)
		}
		return
	}
	t.Fatal("expected storm block")
}

func TestScenario_WorkersZeroUsesGraceNotStall(t *testing.T) {
	s := newSoftSim(t, time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC))
	s.workers = 0
	s.lastTrafficAt = s.now.Add(-trafficStallThreshold - time.Minute)
	res := s.tick()
	if res.Action == softTickStallSoft {
		t.Fatal("workers=0 must not use stall path")
	}
}

func TestScenario_CoreEndAutoSoft(t *testing.T) {
	if !shouldAutoSoftOnCoreEnd(false, false, false, true, true) {
		t.Fatal("CORE die + TUN → soft")
	}
	if shouldAutoSoftOnCoreEnd(false, false, true, true, true) {
		t.Fatal("user Stop must not auto-soft")
	}
}

func TestScenario_KeepaliveDoesNotResetStallClock(t *testing.T) {
	start := time.Date(2026, 8, 10, 17, 0, 0, 0, time.UTC)
	at := start
	bytes := int64(0)
	at, bytes, _ = SimTrafficNote(at, bytes, 10<<20, start)
	mid := start
	for i := 0; i < 30; i++ {
		mid = mid.Add(2 * time.Second)
		bytes += 1024
		newAt, newBytes, meaningful := SimTrafficNote(at, bytes-1024, bytes, mid)
		if meaningful {
			t.Fatal("keepalive must not be meaningful")
		}
		if !newAt.Equal(at) {
			t.Fatal("keepalive must not move lastTrafficAt")
		}
		at, bytes = newAt, newBytes
	}
	if mid.Sub(at) < 60*time.Second {
		t.Fatalf("stall clock should keep growing, got %s", mid.Sub(at))
	}
}

func TestScenario_PreserveRaceReadyBeforeRawConfig(t *testing.T) {
	// TunAlreadyReady: READY раньше raw_config — preserve нельзя снимать на READY.
	if shouldClearSoftPreserveOnWorkerReady() {
		t.Fatal("READY must keep preserve")
	}
	if !decideSoftApplyPath(true, true) {
		t.Fatal("preserve+TUN → soft apply (no Creating adapter)")
	}
	if shouldSkipFinishSoftRecover(true, false) != true {
		t.Fatal("dying session must skip finish")
	}
	if shouldSkipFinishSoftRecover(true, true) {
		t.Fatal("after Start finish allowed")
	}
}
