package core

import (
	"net"
	"sync"
	"time"
)

// relay_health: учёт «живучести» и RTT конкретных VK TURN-серверов.
//
// Каждый воркер = отдельная TURN-аллокация. На флакающих VK-relay аллокация
// живёт 30-90с и умирает, заставляя воркер пересоздаваться (churn). Чтобы
// уменьшить черн, мы считаем среднюю длительность жизни сессий по хосту relay
// (EWMA) и при выборе TURN URL уводим воркеры с явно «дохлых» серверов на более
// стабильные, не пере-выбирая только что умерший relay.
//
// avgRttMs — EWMA времени TURN Allocate + DTLS handshake; ниже RTT → выше score.

const (
	relayShortLifeSec = 50.0
	relayEWMAAlpha    = 0.4
	relayRttEWMAAlpha = 0.35
	relayHotDeadWindow = 25 * time.Second
	relayRttBonusCap   = 150.0
	relayRttPenaltyFloor = -100.0
)

type relayHealth struct {
	avgLifeSec  float64 // EWMA полезной жизни сессии, сек
	avgRttMs    float64 // EWMA TURN+DTLS setup, мс
	shortStreak int
	samples     int
	lastDeath   time.Time
}

var (
	relayHealthMu  sync.Mutex
	relayHealthMap = map[string]*relayHealth{}
)

func relayHostKey(turnURL string) string {
	if host, _, err := net.SplitHostPort(turnURL); err == nil {
		return host
	}
	return turnURL
}

func recordRelaySession(turnURL string, life time.Duration, becameReady bool) {
	key := relayHostKey(turnURL)
	sec := 0.0
	if becameReady {
		sec = life.Seconds()
	}

	relayHealthMu.Lock()
	defer relayHealthMu.Unlock()

	h := relayHealthMap[key]
	if h == nil {
		h = &relayHealth{avgLifeSec: sec}
		relayHealthMap[key] = h
	} else {
		h.avgLifeSec = relayEWMAAlpha*sec + (1-relayEWMAAlpha)*h.avgLifeSec
	}
	h.samples++
	if sec < relayShortLifeSec {
		h.shortStreak++
		h.lastDeath = time.Now()
	} else {
		h.shortStreak = 0
	}
}

// recordRelayPathRTT обновляет EWMA RTT пути (TURN Allocate + DTLS HS) для relay.
func recordRelayPathRTT(turnURL string, rttMs float64) {
	if rttMs <= 0 {
		return
	}
	key := relayHostKey(turnURL)
	relayHealthMu.Lock()
	defer relayHealthMu.Unlock()

	h := relayHealthMap[key]
	if h == nil {
		h = &relayHealth{avgRttMs: rttMs}
		relayHealthMap[key] = h
		return
	}
	if h.avgRttMs <= 0 {
		h.avgRttMs = rttMs
	} else {
		h.avgRttMs = relayRttEWMAAlpha*rttMs + (1-relayRttEWMAAlpha)*h.avgRttMs
	}
}

func relayScore(turnURL string, now time.Time) float64 {
	key := relayHostKey(turnURL)
	h := relayHealthMap[key]
	if h == nil {
		return relayShortLifeSec
	}
	score := h.avgLifeSec
	if !h.lastDeath.IsZero() && now.Sub(h.lastDeath) < relayHotDeadWindow {
		score -= 1000
	}
	score -= float64(h.shortStreak) * 5
	if h.avgRttMs > 0 {
		bonus := 200.0 - h.avgRttMs
		if bonus > relayRttBonusCap {
			bonus = relayRttBonusCap
		}
		if bonus < relayRttPenaltyFloor {
			bonus = relayRttPenaltyFloor
		}
		score += bonus
	}
	return score
}

func pickHealthyTurnURL(urls []string, sessionID int) string {
	if len(urls) == 0 {
		return ""
	}
	if len(urls) == 1 {
		return urls[0]
	}

	relayHealthMu.Lock()
	defer relayHealthMu.Unlock()

	now := time.Now()
	bestIdx, bestScore := -1, -1e18
	n := len(urls)
	for i := 0; i < n; i++ {
		idx := (sessionID + i) % n
		score := relayScore(urls[idx], now)
		if i == 0 {
			score += 0.5
		}
		if score > bestScore {
			bestScore, bestIdx = score, idx
		}
	}
	return urls[bestIdx]
}
