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

// groupPhaseOffset — фазовый сдвиг старта между группами. VK гасит ВСЕ аллокации
// под одним call-credential пачкой при его истечении (~60–90 c). Сдвиг ~полжизни
// креда между группами, чтобы пока одна пересоздаётся, другая держит туннель.
const groupPhaseOffset = 35 * time.Second

// credPoolSizeForWorkers — число независимых VK call-credential на группу.
// Формула как в anton48/vk-turn-proxy-ios (poolSizeForNumConns): при 9 воркерах
// → 4 слота (~2–3 воркера на кред). Когда один кред истекает, умирают не все 9
// воркеров группы, а только его слот — агрегат остаётся стабильным.
func credPoolSizeForWorkers(n int) int {
	if n <= 0 {
		return 2
	}
	size := (n*2 + 4) / 5
	if size < 2 {
		size = 2
	}
	if size > n {
		size = n
	}
	return size
}

// WorkerGroup:
// Запускает N потоков с пулом call-credential (несколько кредов на группу).
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

	// Фазовый сдвиг: десинхронизируем жизнь кредов разных групп, чтобы они не
	// умирали пачкой одновременно (см. groupPhaseOffset). Группа #1 стартует
	// сразу и сразу несёт трафик; следующие — со сдвигом.
	if groupID > 1 {
		offset := time.Duration(groupID-1) * groupPhaseOffset
		log.Printf("[ГРУППА #%d] Фазовый сдвиг старта %s (десинхрон жизни кредов с другими группами)", groupID, offset)
		select {
		case <-time.After(offset):
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

	// Лимит воркеров снят: запускаем всё запрошенное число потоков.
	activeWorkerIDs := workerIDs
	poolSize := credPoolSizeForWorkers(len(activeWorkerIDs))

	// Пул call-credential: несколько независимых VK-кредов на группу (как credPool
	// в anton48/vk-turn-proxy-ios). Воркеры распределены по слотам — при истечении
	// одного креда умирает только его слот, не вся группа разом.
	fetchCredSlot := func(slot int) (*Credentials, error) {
		streamID := groupID*100 + slot
		for {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			credsCtx, credsCancel := context.WithTimeout(context.Background(), 120*time.Second)
			go func() {
				select {
				case <-ctx.Done():
					credsCancel()
				case <-credsCtx.Done():
				}
			}()
			user, pass, turnURLs, err := GetCreds(credsCtx, hash, streamID, captchaResultChan, getCaptchaMode, emitCaptchaRequest)
			credsCancel()
			if err == nil {
				return &Credentials{User: user, Pass: pass, TurnURLs: turnURLs, CacheStreamID: streamID}, nil
			}
			log.Printf("[ГРУППА #%d] Ошибка кредов (слот %d): %v", groupID, slot, err)
			if strings.Contains(err.Error(), "FATAL_AUTH") || strings.Contains(err.Error(), "context canceled") {
				return nil, err
			}
			wait := 15 * time.Second
			if strings.Contains(err.Error(), "CAPTCHA_WAIT_REQUIRED") {
				wait = 65 * time.Second
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	log.Printf("[ГРУППА #%d] Запрос кредов (хеш: %s..., pool=%d слотов)", groupID, shortHash, poolSize)
	credSlots := make([]*Credentials, poolSize)
	for s := 0; s < poolSize; s++ {
		if s > 0 {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
		}
		c, err := fetchCredSlot(s)
		if err != nil {
			return
		}
		credSlots[s] = c
	}

	log.Printf("[ГРУППА #%d] Креды OK, pool=%d, TURN: %v, %d воркеров",
		groupID, poolSize, credSlots[0].TurnURLs, len(activeWorkerIDs))

	if onTurnURLs != nil {
		seen := make(map[string]struct{})
		var all []string
		for _, c := range credSlots {
			if c == nil {
				continue
			}
			for _, u := range c.TurnURLs {
				if _, ok := seen[u]; !ok {
					seen[u] = struct{}{}
					all = append(all, u)
				}
			}
		}
		onTurnURLs(all)
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

	refreshCredSlot := func(slot int, reason string) bool {
		refreshMu.Lock()
		defer refreshMu.Unlock()

		now := time.Now().Unix()
		last := lastCredRefresh.Load()
		minGap := int64(15)
		if strings.Contains(strings.ToLower(reason), "quota") {
			minGap = 30
		}
		if last > 0 && now-last < minGap {
			log.Printf("[TURN] Слот %d: креды уже обновлялись %d сек назад, ждём (%s)", slot, now-last, reason)
			return false
		}

		streamID := groupID*100 + slot
		getStreamCache(streamID).invalidate(streamID)
		refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer refreshCancel()
		u, p, urls, refreshErr := GetCreds(refreshCtx, hash, streamID, captchaResultChan, getCaptchaMode, emitCaptchaRequest)
		if refreshErr != nil {
			log.Printf("[TURN] Слот %d: не удалось обновить креды после %s: %v", slot, reason, refreshErr)
			return false
		}

		credsMu.Lock()
		credSlots[slot] = &Credentials{User: u, Pass: p, TurnURLs: urls, CacheStreamID: streamID}
		credsMu.Unlock()
		lastCredRefresh.Store(time.Now().Unix())
		log.Printf("[TURN] Слот %d: креды обновлены после %s, TURN urls=%d", slot, reason, len(urls))
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

		// Stagger: первые 6 воркеров — 1.2s (как раньше), остальные — медленнее
		// (как slowStagger в anton48), чтобы не штурмовать VK Allocate.
		workerDelay := time.Duration(i) * 1200 * time.Millisecond
		if i >= 6 {
			workerDelay = 6*1200*time.Millisecond + time.Duration(i-5)*3*time.Second
		}
		credSlot := i % poolSize

		go func(wid int, slot int, delay time.Duration) {
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
			// Постоянная фаза воркера: разносит 16-секундные рециклы внутри группы,
			// чтобы воркеры не пересоздавались синхронной волной.
			workerPhase := time.Duration(rand.Intn(3500)) * time.Millisecond

			for {
				if ctx.Err() != nil {
					return
				}
				if !waitQuotaBackoff(wid) {
					return
				}

				credsMu.RLock()
				slotCreds := credSlots[slot]
				if slotCreds == nil {
					credsMu.RUnlock()
					return
				}
				credsSnapshot := *slotCreds
				credsSnapshot.TurnURLs = cloneStringSlice(slotCreds.TurnURLs)
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
					delay := base + workerPhase + time.Duration(wid%workersPerGroup)*400*time.Millisecond + time.Duration(rand.Intn(800))*time.Millisecond
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
						refreshCredSlot(slot, "TURN Allocate attribute-not-found")
					} else if turnCredRefreshNeeded {
						isQuota := strings.Contains(errStrLower, "turn квота") ||
							strings.Contains(errStrLower, "quota") ||
							strings.Contains(errStrLower, "486")
						if isQuota {
							setQuotaBackoff(60)
						}
						log.Printf("[ВОРКЕР #%d] [TURN] Ошибка allocation/кредов, обновляем TURN-креды и повторяем (попытка %d): %s", wid, attempt, errStr)
						refreshCredSlot(slot, "TURN allocation error")
					} else {
						log.Printf("[ВОРКЕР #%d] Ошибка (попытка %d): %s", wid, attempt, errStr)
						if attempt >= 3 && strings.Contains(errStrLower, "retransmissions failed") {
							refreshCredSlot(slot, "TURN Allocate: хост не отвечает")
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
		}(wid, credSlot, workerDelay)
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


