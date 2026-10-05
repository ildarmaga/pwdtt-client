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

const maxWorkers = 108

// NormalizeWorkers rounds the requested worker count to a valid group size (9…108).
func NormalizeWorkers(workers int) int {
	n := workers
	if n <= 0 {
		n = workersPerGroup
	}
	if n > maxWorkers {
		n = maxWorkers
	}
	if n < workersPerGroup {
		n = workersPerGroup
	}
	return (n / workersPerGroup) * workersPerGroup
}

// Как CSQTT: один TURN-набор на две группы (18 аллокаций). Хеш берётся из панели
// или подписки, звонок клиент не создаёт. Новый запрос к VK — только после
// исчерпания этих 18 аллокаций или ответа STUN 401 / 438 / 441.
const (
	groupsPerCredential  = 2
	workersPerCredential = workersPerGroup * groupsPerCredential
	workerStartInterval  = 100 * time.Millisecond
)

func credentialCohort(groupID int) int {
	if groupID < 1 {
		groupID = 1
	}
	return (groupID - 1) / groupsPerCredential
}

func credentialStreamID(cohort int) int {
	if cohort < 0 {
		cohort = 0
	}
	return (cohort + 1) * 100
}

// turnCredRejected — VK отозвал логин TURN. Обычный обрыв relay сюда не входит.
func turnCredRejected(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "error 401") ||
		strings.Contains(s, "error 438") ||
		strings.Contains(s, "error 441") ||
		strings.Contains(s, "unauthorized") ||
		strings.Contains(s, "stale nonce") ||
		strings.Contains(s, "wrong credential") ||
		strings.Contains(s, "invalid credential")
}

type credCohortState struct {
	mu       sync.Mutex
	cond     *sync.Cond
	creds    *Credentials
	attempts int
	fetching bool
	gen      int
}

func newCredCohortState() *credCohortState {
	s := &credCohortState{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

var credCohortStore sync.Map

func cohortForGroup(groupID int) *credCohortState {
	id := credentialCohort(groupID)
	if v, ok := credCohortStore.Load(id); ok {
		return v.(*credCohortState)
	}
	fresh := newCredCohortState()
	actual, _ := credCohortStore.LoadOrStore(id, fresh)
	return actual.(*credCohortState)
}

func (s *credCohortState) invalidate() {
	s.mu.Lock()
	s.gen++
	s.creds = nil
	s.attempts = 0
	s.mu.Unlock()
	s.cond.Broadcast()
}

// lease отдаёт текущий набор, пока не набралось workersPerCredential аллокаций.
// Следующий вызов один раз зовёт fetch; параллельные вызовы ждут тот же результат.
func (s *credCohortState) lease(fetch func() (*Credentials, error)) (*Credentials, error) {
	s.mu.Lock()
	for {
		if s.creds != nil && s.attempts < workersPerCredential {
			s.attempts++
			snap := cloneCreds(s.creds)
			s.mu.Unlock()
			return snap, nil
		}
		if s.fetching {
			s.cond.Wait()
			continue
		}
		gen := s.gen
		s.creds = nil
		s.attempts = 0
		s.fetching = true
		s.mu.Unlock()

		got, err := fetch()

		s.mu.Lock()
		s.fetching = false
		if s.gen != gen {
			s.cond.Broadcast()
			continue
		}
		if err != nil {
			s.cond.Broadcast()
			s.mu.Unlock()
			return nil, err
		}
		s.creds = got
		s.attempts = 1
		snap := cloneCreds(got)
		s.cond.Broadcast()
		s.mu.Unlock()
		return snap, nil
	}
}

func cloneCreds(c *Credentials) *Credentials {
	if c == nil {
		return nil
	}
	return &Credentials{
		User:          c.User,
		Pass:          c.Pass,
		TurnURLs:      cloneStringSlice(c.TurnURLs),
		CacheStreamID: c.CacheStreamID,
	}
}

// WorkerGroup запускает N потоков на одном TURN-наборе когорты (две группы = 18 аллокаций).
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
	sharedGate *wgConfigGate, // RAW: общий gate на все группы; WG: nil
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
	waitTunReady func(ctx context.Context) bool,
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
	cfgGate := sharedGate
	if cfgGate == nil {
		cfgGate = newWGConfigGate(
			configCh,
			tp.TunnelMode,
			tp.MTU,
			tp.RawPrimaryIP,
			rawChunkedEnabled(tp.TunnelMode, tp.TurnTransport),
			tp.ObfsMode,
		)
	}
	if !getConfig {
		configSent = 1
		// Не трогаем shared RAW gate: иначе группа #2 пометит sent до RAWCONF группы #1.
		if cfgGate != nil && cfgGate.tunnelMode != "raw" && cfgGate.tunnelMode != "csqtt" {
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

	activeWorkerIDs := workerIDs
	cohortID := credentialCohort(groupID)
	streamID := credentialStreamID(cohortID)
	cohort := cohortForGroup(groupID)
	log.Printf("[ГРУППА #%d] Креды когорты %d (хеш: %s..., до %d аллокаций на набор)",
		groupID, cohortID, shortHash, workersPerCredential)

	fetchCohortCreds := func() (*Credentials, error) {
		getStreamCache(streamID).invalidate(streamID)
		credsCtx, credsCancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer credsCancel()
		stop := context.AfterFunc(ctx, credsCancel)
		defer stop()
		user, pass, turnURLs, err := GetCreds(credsCtx, hash, streamID, captchaResultChan, getCaptchaMode, emitCaptchaRequest)
		if err != nil {
			return nil, err
		}
		return &Credentials{User: user, Pass: pass, TurnURLs: turnURLs, CacheStreamID: streamID}, nil
	}

	var wg sync.WaitGroup
	var turnURLsOnce sync.Once
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

	dropCohortCreds := func(reason string) {
		getStreamCache(streamID).invalidate(streamID)
		cohort.invalidate()
		log.Printf("[ГРУППА #%d] TURN-креды сброшены (%s), следующий Allocate запросит VK", groupID, reason)
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

		workerDelay := time.Duration(i) * workerStartInterval

		go func(wid int, delay time.Duration, idx int) {
			defer wg.Done()

			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}

			// Первый воркер первой группы поднимает RAWCONF/TUN; остальные ждут excludes.
			if !(getConfig && idx == 0) && waitTunReady != nil {
				if !waitTunReady(ctx) {
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

				slotCreds, credErr := cohort.lease(fetchCohortCreds)
				if credErr != nil {
					log.Printf("[ГРУППА #%d] Ошибка кредов: %v", groupID, credErr)
					if strings.Contains(credErr.Error(), "FATAL_AUTH") || strings.Contains(credErr.Error(), "context canceled") || ctx.Err() != nil {
						return
					}
					wait := 15 * time.Second
					if strings.Contains(credErr.Error(), "CAPTCHA_WAIT_REQUIRED") {
						wait = 65 * time.Second
					}
					select {
					case <-time.After(wait):
					case <-ctx.Done():
						return
					}
					continue
				}
				turnURLsOnce.Do(func() {
					if onTurnURLs != nil && len(slotCreds.TurnURLs) > 0 {
						onTurnURLs(slotCreds.TurnURLs)
					}
					log.Printf("[ГРУППА #%d] TURN-креды когорты %d: %v", groupID, cohortID, slotCreds.TurnURLs)
				})

				configDelivered, sessErr := RunSession(ctx, tp, peer, d, localPort,
					cfgGate, wid, slotCreds, deviceID, password, stats)

				if shouldGetConfig && configDelivered {
					atomic.StoreInt32(&configSent, 1)
				}

				if sessErr == nil {
					attempt = 0
					// Обрыв relay не трогает креды: следующий Allocate идёт с тем же логином.
					if ctx.Err() != nil {
						return
					}
					retry := workerStartInterval + time.Duration(rand.Intn(50))*time.Millisecond
					select {
					case <-time.After(retry):
					case <-ctx.Done():
						return
					}
					continue
				}

				if ctx.Err() != nil {
					return
				}
				errStr := sessErr.Error()
				errStrLower := strings.ToLower(errStr)

				if strings.Contains(errStrLower, "rate limit") ||
					strings.Contains(errStrLower, "flood control") ||
					strings.Contains(errStrLower, "ip mismatch") ||
					strings.Contains(errStrLower, "error 29") {
					errStr += " (ошибка со стороны ВК)"
				}

				if strings.Contains(errStr, "хеш мёртв") ||
					strings.Contains(errStr, "FATAL_AUTH") {
					relay := ""
					if len(slotCreds.TurnURLs) > 0 {
						relay = relayHostKey(slotCreds.TurnURLs[0])
					}
					log.Printf("[ВОРКЕР #%d] Фатальная ошибка relay=%s: %s", wid, relay, errStr)
					return
				}

				attempt++
				if turnCredRejected(sessErr) {
					log.Printf("[ВОРКЕР #%d] [TURN] STUN отверг креды, запрашиваем VK заново (попытка %d): %s", wid, attempt, errStr)
					dropCohortCreds("stun 401/438/441")
				} else if strings.Contains(errStrLower, "turn квота") ||
					strings.Contains(errStrLower, "quota") ||
					strings.Contains(errStrLower, "486") {
					setQuotaBackoff(60)
					log.Printf("[ВОРКЕР #%d] [TURN] квота, креды оставляем (попытка %d): %s", wid, attempt, errStr)
				} else {
					log.Printf("[ВОРКЕР #%d] Ошибка (попытка %d): %s", wid, attempt, errStr)
				}

				isStunDeath := strings.Contains(errStrLower, "error 29") ||
					strings.Contains(errStrLower, "cannot create socket")
				if isStunDeath {
					relay := ""
					if len(slotCreds.TurnURLs) > 0 {
						relay = relayHostKey(slotCreds.TurnURLs[0])
					}
					log.Printf("[ВОРКЕР #%d] Невосстановимая TURN/STUN relay=%s: %s", wid, relay, errStr)
					return
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
		}(wid, workerDelay, i)
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
	Host          string
	Port          string
	Hashes        []string
	WrapKey       []byte // Password-derived WRAP key (32 bytes), nil = disabled
	ObfsMode      string // audio|video
	TunnelMode    string // wg|raw|csqtt
	TurnTransport string // tcp|udp — канал к TURN (default tcp, как qWDTT 1.4)
	MTU           int    // для RAWCONF
	RawPrimaryIP  string // soft-reconnect: IP сохранённого TUN
}

// Credentials — учетные данные TURN
type Credentials struct {
	User          string
	Pass          string
	TurnURLs      []string
	CacheStreamID int
}
