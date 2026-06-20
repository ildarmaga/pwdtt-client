package core

import (
	"context"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)


const workersPerGroup = 9

// WorkersPerGroup — количество воркеров в одной группе (экспортировано для orchestrator).
const WorkersPerGroup = workersPerGroup

// distinctRelayHosts считает число различных relay-хостов среди TURN URL.
func distinctRelayHosts(urls []string) int {
	if len(urls) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(urls))
	for _, u := range urls {
		if u == "" {
			continue
		}
		seen[relayHostKey(u)] = struct{}{}
	}
	return len(seen)
}

// clampWorkersToURLs ограничивает число воркеров в группе под количество relay-хостов,
// которые выдал VK. Если эндпоинтов мало (1–2), много воркеров наваливаются на них,
// VK упирается в квоту одновременных TURN-аллокаций (error 486) и начинает «жать»
// relay через ~16 с → шторм переподключений. ~3 воркера на хост держатся под квотой.
// При 0 (неизвестно) или ≥3 хостах не зажимаем.
func clampWorkersToURLs(distinctHosts, requested int) int {
	if distinctHosts <= 0 || distinctHosts >= 3 {
		return requested
	}
	limit := distinctHosts * 3 // 1 хост → 3, 2 хоста → 6
	if limit >= requested {
		return requested
	}
	return limit
}

// WorkerGroup:
// Запускает 9 потоков с одними кредами. Ротации нет — работает до смерти воркеров.
func WorkerGroup(
	ctx context.Context,
	groupID int,
	hashIndex int,
	tp *TurnParams,
	peer *net.UDPAddr,
	d *Dispatcher,
	localPort string,
	getConfig bool,
	configCh chan<- string,
	workerIDs []int,
	pauseFlag *int32,
	deviceID, password string,
	stats *Stats,
	waitReady <-chan struct{},
	signalReady chan<- struct{},
	captchaResultChan chan string,
	getCaptchaMode func() string,
	emitCaptchaRequest func(mode, redirectURI, sessionToken string),
	onTurnURLs func(urls []string),
) {
	// Каскадный запуск: ждем свою очередь
	if waitReady != nil {
		log.Printf("[ГРУППА #%d] Ожидание сигнала от предыдущей группы...", groupID)
		select {
		case <-waitReady:
		case <-ctx.Done():
			return
		}
	}

	var configSent int32
	cfgGate := newWGConfigGate(configCh)
	if !getConfig {
		configSent = 1
		if cfgGate != nil {
			cfgGate.sent.Store(1)
		}
	}

	// Doze-mode пауза
	for atomic.LoadInt32(pauseFlag) != 0 {
		if ctx.Err() != nil {
			return
		}
		time.Sleep(1 * time.Second)
	}

	hash := tp.Hashes[hashIndex%len(tp.Hashes)]
	shortHash := hash
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	log.Printf("[ГРУППА #%d] Запрос кредов (хеш: %s...)", groupID, shortHash)

	credStreamID := groupID * 100
	var creds *Credentials
	for {
		if ctx.Err() != nil {
			return
		}
		credsCtx, credsCancel := context.WithTimeout(context.Background(), 120*time.Second)
		go func() {
			select {
			case <-ctx.Done():
				credsCancel()
			case <-credsCtx.Done():
			}
		}()
		user, pass, turnURLs, err := GetCreds(credsCtx, hash, credStreamID, captchaResultChan, getCaptchaMode, emitCaptchaRequest)
		credsCancel()
		if err == nil {
			creds = &Credentials{User: user, Pass: pass, TurnURLs: turnURLs, CacheStreamID: credStreamID}
			break
		}
		log.Printf("[ГРУППА #%d] Ошибка кредов: %v", groupID, err)
		if strings.Contains(err.Error(), "FATAL_AUTH") || strings.Contains(err.Error(), "context canceled") {
			return
		}
		wait := 15 * time.Second
		if strings.Contains(err.Error(), "CAPTCHA_WAIT_REQUIRED") {
			wait = 65 * time.Second
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}

	log.Printf("[ГРУППА #%d] Креды OK, TURN: %v, %d воркеров", groupID, creds.TurnURLs, len(workerIDs))

	if onTurnURLs != nil {
		onTurnURLs(creds.TurnURLs)
	}

	// Адаптивный лимит: если VK выдал мало relay-эндпоинтов, зажимаем число
	// воркеров в группе, чтобы не упереться в квоту одновременных TURN-аллокаций.
	activeWorkerIDs := workerIDs
	if hosts := distinctRelayHosts(creds.TurnURLs); hosts > 0 {
		if eff := clampWorkersToURLs(hosts, len(workerIDs)); eff < len(workerIDs) {
			log.Printf("[ГРУППА #%d] VK выдал relay-хостов: %d — ограничиваю воркеров %d→%d (защита от квоты TURN error 486)",
				groupID, hosts, len(workerIDs), eff)
			activeWorkerIDs = workerIDs[:eff]
		}
	}

	var wg sync.WaitGroup
	var credsMu sync.RWMutex
	var refreshMu sync.Mutex
	var lastCredRefresh atomic.Int64
	var quotaBackoffUntil atomic.Int64
	var signalOnce sync.Once
	fireSignalReady := func() {
		if signalReady == nil {
			return
		}
		signalOnce.Do(func() {
			close(signalReady)
			log.Printf("[ГРУППА #%d] Успешный старт! Передача эстафеты следующей группе...", groupID)
		})
	}

	waitQuotaBackoff := func(wid int) bool {
		until := quotaBackoffUntil.Load()
		if until == 0 {
			return true
		}
		now := time.Now().Unix()
		if now >= until {
			return true
		}
		wait := time.Duration(until-now)*time.Second + time.Duration(rand.Intn(3))*time.Second
		log.Printf("[ВОРКЕР #%d] TURN квота: ждём %s перед повтором", wid, wait.Round(time.Second))
		select {
		case <-time.After(wait):
			return true
		case <-ctx.Done():
			return false
		}
	}

	setQuotaBackoff := func(seconds int64) {
		until := time.Now().Unix() + seconds
		for {
			cur := quotaBackoffUntil.Load()
			if cur >= until {
				return
			}
			if quotaBackoffUntil.CompareAndSwap(cur, until) {
				return
			}
		}
	}

	refreshCreds := func(reason string) bool {
		refreshMu.Lock()
		defer refreshMu.Unlock()

		now := time.Now().Unix()
		last := lastCredRefresh.Load()
		minGap := int64(15)
		if strings.Contains(strings.ToLower(reason), "quota") {
			minGap = 30
		}
		if last > 0 && now-last < minGap {
			log.Printf("[TURN] Креды уже обновлялись %d сек назад, ждём следующий retry (%s)", now-last, reason)
			return false
		}

		getStreamCache(credStreamID).invalidate(credStreamID)
		refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer refreshCancel()
		u, p, urls, refreshErr := GetCreds(refreshCtx, hash, credStreamID, captchaResultChan, getCaptchaMode, emitCaptchaRequest)
		if refreshErr != nil {
			log.Printf("[TURN] Не удалось обновить креды после %s: %v", reason, refreshErr)
			return false
		}

		credsMu.Lock()
		creds = &Credentials{User: u, Pass: p, TurnURLs: urls, CacheStreamID: credStreamID}
		credsMu.Unlock()
		lastCredRefresh.Store(time.Now().Unix())
		log.Printf("[TURN] Креды обновлены после %s, TURN urls=%d", reason, len(urls))
		return true
	}

	// Следующая группа стартует после wg_config или через 2 s (как раньше).
	// Не раньше — иначе обе группы шлют TURN Allocate одновременно и упираются
	// в квоту VK (error 486), из-за чего relay убиваются за ~16 s.
	if signalReady != nil {
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			timer := time.NewTimer(2000 * time.Millisecond)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					fireSignalReady()
					return
				case <-ticker.C:
					if cfgGate != nil && cfgGate.delivered() {
						fireSignalReady()
						return
					}
				}
			}
		}()
	}

	for i, wid := range activeWorkerIDs {
		wg.Add(1)

		// Stagger 1.2 s между воркерами: меньше одновременных TURN Allocate,
		// не упираемся в квоту VK. Трафик всё равно идёт с первого READY+wg_config.
		workerDelay := time.Duration(i) * 1200 * time.Millisecond

		go func(wid int, delay time.Duration) {
			defer wg.Done()

			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}

			shouldGetConfig := getConfig
			attempt := 0

			for {
				if ctx.Err() != nil {
					return
				}
				if !waitQuotaBackoff(wid) {
					return
				}

				credsMu.RLock()
				credsSnapshot := *creds
				credsSnapshot.TurnURLs = cloneStringSlice(creds.TurnURLs)
				credsMu.RUnlock()

				sessStart := time.Now()
				configDelivered, sessErr := RunSession(ctx, tp, peer, d, localPort,
					cfgGate, wid, &credsSnapshot, deviceID, password, stats)
				sessLife := time.Since(sessStart)

				if shouldGetConfig && configDelivered {
					atomic.StoreInt32(&configSent, 1)
				}

				if sessErr == nil {
					attempt = 0
					// База 2с при долгой сессии. Если relay сдох быстро (флак),
					// разносим реаллокацию шире, чтобы не устраивать шторм запросов
					// и дать health-выбору увести воркер на стабильный сервер.
					base := 2 * time.Second
					if sessLife < 60*time.Second {
						base = 6 * time.Second
					}
					delay := base + time.Duration(wid%workersPerGroup)*400*time.Millisecond + time.Duration(rand.Intn(800))*time.Millisecond
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						return
					}
					continue
				}

				if sessErr != nil {
					if ctx.Err() != nil {
						return
					}
					errStr := sessErr.Error()
					errStrLower := strings.ToLower(errStr)

					turnAllocAttrMissing := strings.Contains(errStrLower, "turn allocate") &&
						strings.Contains(errStrLower, "attribute not found")
					turnCredRefreshNeeded := turnAllocAttrMissing ||
						strings.Contains(errStrLower, "turn allocate auth") ||
						strings.Contains(errStrLower, "invalid credential") ||
						strings.Contains(errStrLower, "stale nonce") ||
						strings.Contains(errStrLower, "allocation mismatch") ||
						strings.Contains(errStrLower, "error 508") ||
						strings.Contains(errStrLower, "turn квота") ||
						strings.Contains(errStrLower, "quota")

					if strings.Contains(errStrLower, "rate limit") ||
						strings.Contains(errStrLower, "flood control") ||
						strings.Contains(errStrLower, "ip mismatch") ||
						strings.Contains(errStrLower, "error 29") {
						errStr += " (ошибка со стороны ВК)"
					}

					if strings.Contains(errStr, "хеш мёртв") ||
						strings.Contains(errStr, "FATAL_AUTH") {
						relay := ""
						if len(credsSnapshot.TurnURLs) > 0 {
							relay = relayHostKey(credsSnapshot.TurnURLs[0])
						}
						log.Printf("[ВОРКЕР #%d] Фатальная ошибка relay=%s: %s", wid, relay, errStr)
						return
					}

					attempt++
					if turnAllocAttrMissing {
						log.Printf("[ВОРКЕР #%d] [TURN] Allocate вернул неполный ответ, обновляем TURN-креды и повторяем (попытка %d): %s", wid, attempt, errStr)
						refreshCreds("TURN Allocate attribute-not-found")
					} else if turnCredRefreshNeeded {
						isQuota := strings.Contains(errStrLower, "turn квота") ||
							strings.Contains(errStrLower, "quota") ||
							strings.Contains(errStrLower, "486")
						if isQuota {
							setQuotaBackoff(60)
						}
						log.Printf("[ВОРКЕР #%d] [TURN] Ошибка allocation/кредов, обновляем TURN-креды и повторяем (попытка %d): %s", wid, attempt, errStr)
						refreshCreds("TURN allocation error")
					} else {
						log.Printf("[ВОРКЕР #%d] Ошибка (попытка %d): %s", wid, attempt, errStr)
						// «all retransmissions failed» = VK-хост не отвечает на Allocate
						// (мёртвый relay). relay-health уже уводит воркер на другой хост,
						// но если в группе все URL мёртвые — после пары попыток обновляем
						// креды, чтобы VK выдал свежий набор TURN-хостов.
						if attempt >= 3 && strings.Contains(errStrLower, "retransmissions failed") {
							refreshCreds("TURN Allocate: хост не отвечает")
						}
					}

					// Если ошибка STUN (credentials invalid), воркер не сможет переподключиться. Завершаем.
					isStunDeath := strings.Contains(errStrLower, "error 29") ||
						strings.Contains(errStrLower, "cannot create socket")

					if isStunDeath {
						relay := ""
						if len(credsSnapshot.TurnURLs) > 0 {
							relay = relayHostKey(credsSnapshot.TurnURLs[0])
						}
						log.Printf("[ВОРКЕР #%d] Невосстановимая TURN/STUN relay=%s: %s", wid, relay, errStr)
						return
					}
				}

				if ctx.Err() != nil {
					return
				}

				retryDelay := time.Duration(min(2<<uint(attempt-1), 30)) * time.Second
				errLower := strings.ToLower(sessErr.Error())
				if strings.Contains(errLower, "wrap_auth_timeout") ||
					strings.Contains(errLower, "dtls timeout") ||
					strings.Contains(errLower, "dtls хендшейк") {
					retryDelay = 2*time.Second + time.Duration(rand.Intn(2))*time.Second
				}
				if strings.Contains(errLower, "quota") ||
					strings.Contains(errLower, "486") ||
					strings.Contains(errLower, "turn квота") {
					retryDelay = 60*time.Second + time.Duration(rand.Intn(15))*time.Second
				}
				retryDelay += time.Duration(rand.Intn(3)) * time.Second
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					return
				}
			}
		}(wid, workerDelay)
	}

	wg.Wait()
	log.Printf("[ГРУППА #%d] Все воркеры группы завершились.", groupID)
}

// ParseHashes — парсит строку хешей
func ParseHashes(raw string) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, h := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		h = normalizeVKJoinHash(h)
		if h != "" {
			if _, exists := seen[h]; exists {
				continue
			}
			seen[h] = struct{}{}
			result = append(result, h)
		}
	}
	return result
}

func normalizeVKJoinHash(input string) string {
	s := strings.Trim(strings.TrimSpace(input), "<>\"'")
	if s == "" {
		return ""
	}

	lower := strings.ToLower(s)
	if idx := strings.Index(lower, "/call/join/"); idx >= 0 {
		s = s[idx+len("/call/join/"):]
	} else if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return ""
	}

	if idx := strings.IndexAny(s, "?#/"); idx != -1 {
		s = s[:idx]
	}
	return strings.Trim(strings.TrimSpace(s), "/")
}

// TurnParams — конфигурация TURN
type TurnParams struct {
	Host    string
	Port    string
	Hashes  []string
	WrapKey []byte // Password-derived WRAP key (32 bytes), nil = disabled
}

// Credentials — учетные данные TURN
type Credentials struct {
	User          string
	Pass          string
	TurnURLs      []string
	CacheStreamID int
}


