package core

import (
	"net"
	"sync"
	"time"
)

// relay_health: учёт «живучести» конкретных VK TURN-серверов.
//
// Каждый воркер = отдельная TURN-аллокация. На флакающих VK-relay аллокация
// живёт 30-90с и умирает, заставляя воркер пересоздаваться (churn). Чтобы
// уменьшить черн, мы считаем среднюю длительность жизни сессий по хосту relay
// (EWMA) и при выборе TURN URL уводим воркеры с явно «дохлых» серверов на более
// стабильные, не пере-выбирая только что умерший relay.

const (
	// Сессия короче этого порога считается «быстрой смертью» (флак relay).
	relayShortLifeSec = 50.0
	// Коэффициент сглаживания EWMA: выше = быстрее реагируем на свежие данные.
	relayEWMAAlpha = 0.4
	// Окно, в течение которого только что умерший relay считаем «горячим».
	relayHotDeadWindow = 25 * time.Second
)

type relayHealth struct {
	avgLifeSec  float64 // EWMA полезной жизни сессии, сек
	shortStreak int     // подряд коротких сессий
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

// recordRelaySession обновляет статистику relay после завершения сессии.
// becameReady=false → сессия умерла до выхода в READY (считаем как смерть, life=0).
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

// relayScore — чем больше, тем здоровее relay. Неизвестный relay получает
// оптимистичную оценку, чтобы дать ему шанс.
func relayScore(turnURL string, now time.Time) float64 {
	key := relayHostKey(turnURL)
	h := relayHealthMap[key]
	if h == nil {
		return relayShortLifeSec
	}
	score := h.avgLifeSec
	if !h.lastDeath.IsZero() && now.Sub(h.lastDeath) < relayHotDeadWindow {
		score -= 1000 // только что сдох — избегаем
	}
	score -= float64(h.shortStreak) * 5
	return score
}

// pickHealthyTurnURL выбирает самый «живучий» TURN URL из списка.
// База — детерминированное распределение по sessionID (чтобы не свалить все
// воркеры на один relay), но при равных условиях/свежей смерти уводим воркер
// на более стабильный сервер.
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
		// стартуем перебор со «своего» relay (spread по sessionID)
		idx := (sessionID + i) % n
		score := relayScore(urls[idx], now)
		// лёгкий приоритет «своему» relay при равенстве, чтобы не дёргать выбор
		if i == 0 {
			score += 0.5
		}
		if score > bestScore {
			bestScore, bestIdx = score, idx
		}
	}
	return urls[bestIdx]
}
