package core

import (
	"testing"
	"time"
)

func resetRelayHealth() {
	relayHealthMu.Lock()
	relayHealthMap = map[string]*relayHealth{}
	relayHealthMu.Unlock()
}

func TestRelayHostKey(t *testing.T) {
	if got := relayHostKey("91.231.135.89:19302"); got != "91.231.135.89" {
		t.Fatalf("relayHostKey host:port = %q, want 91.231.135.89", got)
	}
	if got := relayHostKey("no-port"); got != "no-port" {
		t.Fatalf("relayHostKey without port = %q, want passthrough", got)
	}
}

func TestRecordRelaySessionShortStreak(t *testing.T) {
	resetRelayHealth()
	url := "10.0.0.1:19302"
	for i := 0; i < 3; i++ {
		recordRelaySession(url, 10*time.Second, true) // короткие сессии
	}
	relayHealthMu.Lock()
	h := relayHealthMap[relayHostKey(url)]
	relayHealthMu.Unlock()
	if h == nil {
		t.Fatal("нет записи health")
	}
	if h.shortStreak != 3 {
		t.Fatalf("shortStreak = %d, want 3", h.shortStreak)
	}
	if h.avgLifeSec >= relayShortLifeSec {
		t.Fatalf("avgLifeSec = %.1f, ожидалось < %.0f", h.avgLifeSec, relayShortLifeSec)
	}

	// Долгая сессия сбрасывает streak.
	recordRelaySession(url, 5*time.Minute, true)
	relayHealthMu.Lock()
	h = relayHealthMap[relayHostKey(url)]
	relayHealthMu.Unlock()
	if h.shortStreak != 0 {
		t.Fatalf("shortStreak после долгой сессии = %d, want 0", h.shortStreak)
	}
}

func TestRecordRelaySessionNotReadyIsDeath(t *testing.T) {
	resetRelayHealth()
	url := "10.0.0.2:19302"
	// becameReady=false → life игнорируется, считается смертью (life=0 < порога).
	recordRelaySession(url, 10*time.Minute, false)
	relayHealthMu.Lock()
	h := relayHealthMap[relayHostKey(url)]
	relayHealthMu.Unlock()
	if h == nil || h.shortStreak != 1 {
		t.Fatalf("смерть на хендшейке должна давать shortStreak=1, got %+v", h)
	}
}

func TestPickHealthyTurnURLSteersToStable(t *testing.T) {
	resetRelayHealth()
	good := "1.1.1.1:19302"
	bad := "2.2.2.2:19302"
	urls := []string{good, bad}

	// good — долгие сессии, bad — постоянная быстрая смерть.
	for i := 0; i < 5; i++ {
		recordRelaySession(good, 4*time.Minute, true)
		recordRelaySession(bad, 8*time.Second, true)
	}

	// Воркер, чей sessionID указывает на bad (idx 1), должен быть уведён на good.
	got := pickHealthyTurnURL(urls, 1)
	if got != good {
		t.Fatalf("pick = %q, ожидался стабильный %q", got, good)
	}
	// И воркер с базой на good остаётся на good.
	if got := pickHealthyTurnURL(urls, 0); got != good {
		t.Fatalf("pick(sid=0) = %q, ожидался %q", got, good)
	}
}

func TestPickHealthyTurnURLAvoidsHotDead(t *testing.T) {
	resetRelayHealth()
	a := "3.3.3.3:19302"
	b := "4.4.4.4:19302"
	urls := []string{a, b}

	// Оба неизвестны, но a только что сдох → должен выбираться b.
	recordRelaySession(a, 5*time.Second, true) // ставит lastDeath = now
	got := pickHealthyTurnURL(urls, 0)          // база на a (idx 0)
	if got != b {
		t.Fatalf("pick = %q, ожидался %q (a только что сдох)", got, a)
	}
}

func TestPickHealthyTurnURLSingleAndEmpty(t *testing.T) {
	resetRelayHealth()
	if got := pickHealthyTurnURL(nil, 0); got != "" {
		t.Fatalf("pick(nil) = %q, want пусто", got)
	}
	one := []string{"5.5.5.5:19302"}
	if got := pickHealthyTurnURL(one, 7); got != one[0] {
		t.Fatalf("pick(single) = %q, want %q", got, one[0])
	}
}

func TestPickHealthyTurnURLSpreadUnknown(t *testing.T) {
	resetRelayHealth()
	urls := []string{"6.6.6.6:19302", "7.7.7.7:19302", "8.8.8.8:19302"}
	// Все relay неизвестны → выбор должен распределяться по sessionID (spread),
	// первый кандидат — «свой» relay sessionID%n.
	counts := map[string]int{}
	for sid := 0; sid < 9; sid++ {
		counts[pickHealthyTurnURL(urls, sid)]++
	}
	for _, u := range urls {
		if counts[u] == 0 {
			t.Fatalf("relay %q ни разу не выбран при равном здоровье (нет spread): %v", u, counts)
		}
	}
}
