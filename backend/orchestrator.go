package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"wg-turn-client/core"
)

// wailsLogWriter перехватывает log.Printf и направляет в Wails-события.
// Буферизует записи и флашит каждые 100ms чтобы не блокировать core.
// Параллельно пишет полный лог в файл ~/.config/pwdtt/logs/<session>.log
type wailsLogWriter struct {
	ctx  context.Context
	mu   sync.Mutex
	buf  []logEntry
	stop chan struct{}
	file *os.File
}

const maxLogBuf = 500

type logEntry struct{ level, msg string }

func newSessionLogFile(peerIP string) *os.File {
	dir := filepath.Join(configDir(), "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil
	}
	ts := time.Now().Format("2006-01-02_15-04-05")
	name := ts + "_" + peerIP + ".log"
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}
	return f
}

func (w *wailsLogWriter) start() {
	w.stop = make(chan struct{})
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				w.flush()
			case <-w.stop:
				w.flush()
				return
			}
		}
	}()
}

func (w *wailsLogWriter) flush() {
	w.mu.Lock()
	if len(w.buf) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buf
	w.buf = nil
	w.mu.Unlock()
	for _, e := range batch {
		runtime.EventsEmit(w.ctx, "log", e.level, e.msg)
		if friendly := formatConnectionError(e.msg); friendly != "" {
			runtime.EventsEmit(w.ctx, "error", friendly)
		}
	}
}

func formatConnectionError(msg string) string {
	if msg == "" {
		return ""
	}
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "неверный пароль"):
		return "Неверный пароль VPN"
	case strings.Contains(low, "срок действия") && strings.Contains(low, "истёк"):
		return "Срок подписки истёк"
	case strings.Contains(low, "другому устройству") || strings.Contains(low, "device_mismatch"):
		return "Это устройство не привязано к паролю (лимит устройств)"
	case strings.Contains(low, "деактивирован") || strings.Contains(low, "deactivated"):
		return "Пароль деактивирован администратором"
	case strings.Contains(low, "too_many_sessions") || strings.Contains(low, "слишком много параллельных"):
		return "Слишком много параллельных подключений с этого устройства"
	case strings.Contains(low, "traffic_exceeded") || strings.Contains(low, "лимит трафика"):
		return "Лимит трафика исчерпан"
	case strings.Contains(low, "wrap_auth_timeout"):
		return "Мёртвый TURN relay (таймаут DTLS), повтор…"
	case strings.Contains(low, "fatal_auth"):
		if i := strings.Index(low, "fatal_auth:"); i >= 0 {
			tail := strings.TrimSpace(msg[i+len("fatal_auth:"):])
			if tail != "" {
				if again := formatConnectionError(tail); again != "" {
					return again
				}
				return tail
			}
		}
		return "Сервер отклонил подключение"
	default:
		return ""
	}
}

func (w *wailsLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if len(msg) > 20 && msg[4] == '/' {
		msg = strings.TrimSpace(msg[20:])
	}
	level := classifyLevel(msg)

	// Пишем в файл сразу (без буфера)
	if w.file != nil {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(w.file, "[%s] [%s] %s\n", ts, level, msg)
	}

	w.mu.Lock()
	if len(w.buf) >= maxLogBuf {
		// Дропаем старейшую запись чтобы не расти бесконечно
		w.buf = w.buf[1:]
	}
	w.buf = append(w.buf, logEntry{level, msg})
	w.mu.Unlock()
	return len(p), nil
}

func classifyLevel(msg string) string {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "fatal_auth") ||
		strings.Contains(low, "ошибка") ||
		strings.Contains(low, "error") ||
		strings.Contains(low, "fatal") ||
		strings.Contains(low, "фатальн"):
		return "ERROR"
	case strings.Contains(low, "warn") ||
		strings.Contains(low, "не удалось") ||
		strings.Contains(low, "повторим") ||
		strings.Contains(low, "повторяем") ||
		strings.Contains(low, "retry"):
		return "WARN"
	case strings.Contains(low, "debug") ||
		strings.Contains(low, "obfs") ||
		strings.Contains(low, "unwrap") ||
		strings.Contains(low, "wrap:"):
		return "DEBUG"
	default:
		return "INFO"
	}
}

func configDir() string {
	dir := DataDir()
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func profilePath(name string) string {
	return filepath.Join(configDir(), "profiles", name+".json")
}

// ProfileData — хранится в ~/.config/pwdtt/profiles/<name>.json
type ProfileData struct {
	PeerAddr      string   `json:"peer"`
	Password      string   `json:"password"`
	Hashes        []string `json:"hashes"`
	Listen        string   `json:"listen,omitempty"`
	TurnHost      string   `json:"turn,omitempty"`
	TurnPort      string   `json:"port,omitempty"`
	DeviceID      string   `json:"device_id,omitempty"`
	RawDirectPort int      `json:"raw_port,omitempty"` // 0 = DTLS+3
}

// ConnectParams — runtime параметры от UI.
// Profile — уникальный ключ профиля (id сервера), по нему грузится ProfileData с диска.
// Name — человекочитаемое имя сервера, используется только для имени лог-файла.
type ConnectParams struct {
	Profile         string   `json:"profile"`
	Name            string   `json:"name,omitempty"`
	CaptchaMode     string   `json:"captchaMode"`
	Workers         int      `json:"workers,omitempty"`
	MTU             int      `json:"mtu,omitempty"`
	Hashes          []string `json:"hashes,omitempty"`
	VKThroughTunnel bool     `json:"vkThroughTunnel,omitempty"`
	ObfsMode        string   `json:"obfsMode,omitempty"`       // audio|video
	TunnelMode      string   `json:"tunnelMode,omitempty"`     // wg|raw
	TurnTransport   string   `json:"turnTransport,omitempty"` // tcp|udp (default tcp)
}

func loadProfile(name string) (*ProfileData, error) {
	data, err := os.ReadFile(profilePath(name))
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}
	var p ProfileData
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("profile %q parse: %w", name, err)
	}
	return &p, nil
}

type coreSession struct {
	c      *core.Core
	doneCh <-chan core.Event
	closed chan struct{} // закрывается когда forwardEvents завершился
}

// Orchestrator — тонкий прокси между Wails UI и core.
// Два состояния: sess != nil / nil.
type Orchestrator struct {
	appCtx        context.Context
	mu            sync.Mutex
	sess          *coreSession
	prevLogWriter io.Writer
	onTray        func(connected bool, rx, tx int64, workers int32)
	internetMu    sync.RWMutex
	internetRTTMs float64
	pingStop      chan struct{}
	lastParams    ConnectParams
	tunnelUp             bool
	workersZeroAt        time.Time
	workersLostAt        bool
	suppressWorkersLost  bool
	lastTrafficBytes     int64
	lastTrafficAt        time.Time
	trafficWasActive     bool
	trafficActiveUntil   time.Time
	autoReconnecting     bool
	lastAutoReconnectAt  time.Time
	trafficWatchStop     chan struct{}
	internetProbeFails   int
	lastWorkers          int32
	assignedWorkers      int32
	connectedAt          time.Time
	preserveOnSessionEnd bool
	sessionWatchUntil    time.Time
	netWatchStop         chan struct{}
	// softRecoverUntil — окно после SoftReconnect: не триггерить повторный
	// auto-reconnect, пока идёт VK auth / TURN Allocate (иначе рвём context
	// каждые ~10–12 с и сессия не поднимается без ребута ПК).
	softRecoverUntil time.Time
	// softRecoverVKDirect — во время soft-recover VK веб/API временно напрямую;
	// после первого живого воркера вернём «через туннель».
	softRecoverVKDirect bool
	// softRecoverCount — сколько soft подряд (диагностика; эскалации в full нет).
	softRecoverCount int
	// softRecoverVerifyUntil — дедлайн проверки трафика после soft.
	softRecoverVerifyUntil time.Time
	// softRecoverBytesAt — суммарный rx+tx на старте verify-окна.
	softRecoverBytesAt int64
	// softSwapInProgress — SoftReconnect между stop старой и Start новой сессии.
	softSwapInProgress bool
}

const networkChangeDebounce = 1500 * time.Millisecond

const workersLostGrace = 4 * time.Second

const (
	trafficStallThreshold    = 8 * time.Second // залипание пути (игры не терпят 20–30 с)
	trafficActiveMinBytes    = int64(512)      // мелкие игровые пакеты
	trafficActiveWindow      = 3 * time.Minute
	sessionWatchAfterConnect = 3 * time.Minute // после Connect всегда следим за залипанием
	autoReconnectCooldown    = 10 * time.Second
	autoReconnectProbeEvery  = 1 * time.Second
	internetProbeInterval    = 2 * time.Second
	internetProbeTimeout     = 1500 * time.Millisecond
	internetProbeFailNeed    = 2
	// softRecoverAuthGrace — VK Calls + DNS + TURN Allocate при «мёртвом» WG
	// легко занимают 30–60 с; 4 с workersLostGrace этого не покрывает.
	softRecoverAuthGrace = 90 * time.Second
)

func NewOrchestrator(ctx context.Context, onTray func(bool, int64, int64, int32)) *Orchestrator {
	return &Orchestrator{appCtx: ctx, onTray: onTray}
}

// SetVKThroughTunnel переключает маршрут VK и сохраняет выбор для auto-reconnect.
func (o *Orchestrator) SetVKThroughTunnel(through bool) error {
	o.mu.Lock()
	o.lastParams.VKThroughTunnel = through
	o.mu.Unlock()
	return SetVKThroughTunnel(through)
}

func (o *Orchestrator) Reconnect() error {
	o.mu.Lock()
	params := o.lastParams
	o.softRecoverUntil = time.Time{}
	o.softRecoverVKDirect = false
	o.softRecoverCount = 0
	o.softRecoverVerifyUntil = time.Time{}
	o.softRecoverBytesAt = 0
	o.mu.Unlock()
	SetSoftReconnectPreserve(false)
	if params.Profile == "" {
		return fmt.Errorf("нет сохранённых параметров подключения")
	}
	if o.IsRunning() {
		o.stopCoreSession(true)
	}
	o.resetWorkersLostState()
	return o.Start(params)
}

// SoftReconnect — перезапуск TURN/core без сноса wg-turn (быстрое «оживление»).
func (o *Orchestrator) SoftReconnect() error {
	o.mu.Lock()
	params := o.lastParams
	canPreserve := o.tunnelUp && wgTunnelActive()
	// Уже идёт soft и сессия жива — не убивать TURN/VK mid-flight.
	if softRecoverBusy(o.softRecoverVKDirect, o.softRecoverUntil, time.Now()) &&
		o.sess != nil && o.sess.c != nil {
		o.mu.Unlock()
		return fmt.Errorf("soft-восстановление уже идёт")
	}
	if o.softSwapInProgress {
		o.mu.Unlock()
		return fmt.Errorf("soft-восстановление уже идёт")
	}
	o.softSwapInProgress = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.softSwapInProgress = false
		o.mu.Unlock()
	}()
	if params.Profile == "" {
		return fmt.Errorf("нет сохранённых параметров подключения")
	}
	if canPreserve {
		// Держим флаг до первого живого воркера (finishSoftRecoverVK) —
		// нельзя сбрасывать defer'ом сразу после Start: иначе wg_config
		// приходит уже без preserve и сносит wg-turn посреди VK auth.
		SetSoftReconnectPreserve(true)
		// WG жив как интерфейс, но TURN уже мёртв → VK API/DNS через туннель
		// дают i/o timeout / no such host и auth никогда не завершается.
		if err := SetVKThroughTunnel(false); err != nil {
			runtime.EventsEmit(o.appCtx, "log", "WARN", fmt.Sprintf("[SOFT] VK напрямую: %v", err))
		} else {
			runtime.EventsEmit(o.appCtx, "log", "INFO", "[SOFT] VK временно напрямую (auth/DNS), пока поднимаются TURN-воркеры")
		}
		// Блокируем nested soft сразу, до stop старой сессии.
		o.mu.Lock()
		o.softRecoverCount++
		o.softRecoverUntil = time.Now().Add(softRecoverAuthGrace)
		o.softRecoverVKDirect = true
		o.mu.Unlock()
	}
	if o.IsRunning() {
		o.stopCoreSession(!canPreserve)
		o.waitSessionEnd(15 * time.Second)
	}
	o.resetWorkersLostState()
	o.mu.Lock()
	o.lastTrafficBytes = 0
	o.lastTrafficAt = time.Time{}
	o.internetProbeFails = 0
	o.softRecoverVerifyUntil = time.Time{}
	o.softRecoverBytesAt = 0
	if canPreserve {
		o.softRecoverUntil = time.Now().Add(softRecoverAuthGrace)
		o.softRecoverVKDirect = true
	} else {
		o.softRecoverUntil = time.Time{}
		o.softRecoverVKDirect = false
		SetSoftReconnectPreserve(false)
	}
	o.mu.Unlock()
	err := o.Start(params)
	if err != nil {
		SetSoftReconnectPreserve(false)
		o.mu.Lock()
		o.softRecoverUntil = time.Time{}
		o.softRecoverVKDirect = false
		o.mu.Unlock()
		return err
	}
	// RAW: core уже слушает новый :9000 — сразу переподключить bridge,
	// не ждать raw_config (иначе TUN жив, а пакеты в никуда).
	if canPreserve && params.TunnelMode == "raw" {
		if rerr := rebindRawBridgeSoft("9000"); rerr != nil {
			runtime.EventsEmit(o.appCtx, "log", "WARN", fmt.Sprintf("[RAW] Soft bridge rebind: %v", rerr))
		}
	}
	return nil
}

func (o *Orchestrator) inSoftRecoverWindow() bool {
	return !o.softRecoverUntil.IsZero() && time.Now().Before(o.softRecoverUntil)
}

// finishSoftRecoverVK — после первого живого воркера вернуть VK веб/API в туннель
// и снять SoftReconnectPreserve (можно снова применять wg_config).
func (o *Orchestrator) finishSoftRecoverVK() {
	o.mu.Lock()
	need := o.softRecoverVKDirect || SoftReconnectPreserve()
	startVerify := need && o.softRecoverCount > 0 && o.softRecoverVerifyUntil.IsZero()
	o.softRecoverVKDirect = false
	o.softRecoverUntil = time.Time{}
	if startVerify {
		now := time.Now()
		o.softRecoverVerifyUntil = now.Add(softRecoverVerifyWait)
		o.softRecoverBytesAt = o.lastTrafficBytes
		o.lastTrafficAt = now
		o.sessionWatchUntil = now.Add(sessionWatchAfterConnect)
	}
	o.mu.Unlock()
	if !need {
		return
	}
	SetSoftReconnectPreserve(false)
	if err := SetVKThroughTunnel(true); err != nil {
		runtime.EventsEmit(o.appCtx, "log", "WARN", fmt.Sprintf("[SOFT] не удалось вернуть VK в туннель: %v", err))
		return
	}
	runtime.EventsEmit(o.appCtx, "log", "INFO", "[SOFT] VK снова через туннель (воркеры живы)")
	if startVerify {
		runtime.EventsEmit(o.appCtx, "log", "INFO",
			fmt.Sprintf("[SOFT] Проверка трафика %s (иначе ещё soft)", softRecoverVerifyWait.Round(time.Second)))
	}
}

func (o *Orchestrator) resetWorkersLostState() {
	o.workersZeroAt = time.Time{}
	o.workersLostAt = false
}

func (o *Orchestrator) emitWorkersLost(msg string) {
	if o.workersLostAt {
		return
	}
	o.workersLostAt = true
	runtime.EventsEmit(o.appCtx, "workers_lost", msg)
	runtime.EventsEmit(o.appCtx, "log", "WARN", msg)
}

func (o *Orchestrator) noteWorkerStats(workers int32) {
	o.mu.Lock()
	o.lastWorkers = workers
	tunnelUp := o.tunnelUp
	o.mu.Unlock()
	if !tunnelUp {
		return
	}
	if workers > 0 {
		o.mu.Lock()
		o.workersZeroAt = time.Time{}
		o.workersLostAt = false
		o.mu.Unlock()
		o.finishSoftRecoverVK()
		return
	}
	o.mu.Lock()
	vkDirect := o.softRecoverVKDirect
	until := o.softRecoverUntil
	busy := softRecoverBusy(vkDirect, until, time.Now())
	// Пока soft auth/TURN — продлеваем grace, не стартуем второй SoftReconnect.
	if busy {
		if vkDirect {
			o.softRecoverUntil = time.Now().Add(softRecoverAuthGrace)
		}
		o.mu.Unlock()
		return
	}
	if o.workersZeroAt.IsZero() {
		o.workersZeroAt = time.Now()
		o.mu.Unlock()
		return
	}
	zeroFor := time.Since(o.workersZeroAt)
	o.mu.Unlock()
	if zeroFor >= workersLostGrace {
		o.triggerAutoReconnect("Нет активных воркеров — soft-восстановление…")
	}
}

func (o *Orchestrator) shouldWatchTraffic(now time.Time) bool {
	if o.trafficWasActive && now.Before(o.trafficActiveUntil) {
		return true
	}
	return !o.sessionWatchUntil.IsZero() && now.Before(o.sessionWatchUntil)
}

func (o *Orchestrator) noteTrafficBytes(rx, tx int64) {
	if !o.tunnelUp {
		return
	}
	total := rx + tx
	now := time.Now()
	if o.lastTrafficAt.IsZero() {
		o.lastTrafficBytes = total
		o.lastTrafficAt = now
		return
	}
	if total > o.lastTrafficBytes {
		delta := total - o.lastTrafficBytes
		o.lastTrafficBytes = total
		o.lastTrafficAt = now
		o.internetProbeFails = 0
		if delta >= trafficActiveMinBytes {
			o.trafficWasActive = true
			o.trafficActiveUntil = now.Add(trafficActiveWindow)
		}
		// Soft verify прошёл — сбрасываем счётчик эскалации.
		if !o.softRecoverVerifyUntil.IsZero() &&
			softRecoverTrafficOK(now, o.lastTrafficAt, total-o.softRecoverBytesAt) {
			o.softRecoverCount = 0
			o.softRecoverVerifyUntil = time.Time{}
			o.softRecoverBytesAt = 0
		}
		return
	}
	if o.trafficWasActive && now.After(o.trafficActiveUntil) {
		o.trafficWasActive = false
	}
}

func (o *Orchestrator) triggerAutoReconnect(msg string) { o.triggerReconnect(msg, false) }

// triggerReconnect запускает авто-переподключение. forceFull=true гарантирует
// полный reconnect (с пересборкой маршрутов) — нужен при смене сети, когда
// сменился шлюз и прямые /32-маршруты к TURN устарели.
func (o *Orchestrator) triggerReconnect(msg string, forceFull bool) {
	o.mu.Lock()
	if !o.tunnelUp || o.autoReconnecting || o.suppressWorkersLost {
		o.mu.Unlock()
		return
	}
	// Во время soft-recover auth окна не рвём сессию повторно (кроме forceFull —
	// смена сети/шлюза, когда /32 к TURN устарели).
	if !forceFull && softRecoverBusy(o.softRecoverVKDirect, o.softRecoverUntil, time.Now()) {
		o.mu.Unlock()
		return
	}
	if !o.lastAutoReconnectAt.IsZero() && time.Since(o.lastAutoReconnectAt) < autoReconnectCooldown {
		o.mu.Unlock()
		return
	}
	o.autoReconnecting = true
	o.lastAutoReconnectAt = time.Now()
	mode := decideRecoverMode(forceFull, o.softRecoverCount, o.tunnelUp, wgTunnelActive())
	o.mu.Unlock()

	runtime.EventsEmit(o.appCtx, "log", "WARN", msg)
	runtime.EventsEmit(o.appCtx, "auto_reconnect", msg)

	go func() {
		defer func() {
			o.mu.Lock()
			o.autoReconnecting = false
			o.mu.Unlock()
		}()
		var err error
		if mode == recoverSoft {
			err = o.SoftReconnect()
		} else {
			err = o.Reconnect()
		}
		if err != nil {
			runtime.EventsEmit(o.appCtx, "log", "ERROR", fmt.Sprintf("Авто-переподключение не удалось: %v", err))
			o.emitWorkersLost("Связь потеряна — нажмите «Переподключить».")
		}
	}()
}

func (o *Orchestrator) startTrafficWatch() {
	o.stopTrafficWatch()
	stop := make(chan struct{})
	o.trafficWatchStop = stop
	go func() {
		t := time.NewTicker(autoReconnectProbeEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				o.maybeAutoReconnectOnStall()
			}
		}
	}()
}

func (o *Orchestrator) stopTrafficWatch() {
	if o.trafficWatchStop != nil {
		close(o.trafficWatchStop)
		o.trafficWatchStop = nil
	}
	o.lastTrafficBytes = 0
	o.lastTrafficAt = time.Time{}
	o.trafficWasActive = false
	o.trafficActiveUntil = time.Time{}
	o.autoReconnecting = false
	o.internetProbeFails = 0
	o.sessionWatchUntil = time.Time{}
}

func (o *Orchestrator) maybeAutoReconnectOnStall() {
	o.mu.Lock()
	now := time.Now()
	watch := o.shouldWatchTraffic(now)
	var stallDur time.Duration
	if !o.lastTrafficAt.IsZero() {
		stallDur = now.Sub(o.lastTrafficAt)
	}
	verifyUntil := o.softRecoverVerifyUntil
	bytesSinceSoft := o.lastTrafficBytes - o.softRecoverBytesAt
	lastAt := o.lastTrafficAt
	softCount := o.softRecoverCount
	o.mu.Unlock()

	// Soft поднял воркеров, но данные не пошли — ещё раз soft (с обновлением ключей WG).
	if !verifyUntil.IsZero() && now.After(verifyUntil) {
		if softRecoverTrafficOK(now, lastAt, bytesSinceSoft) {
			o.mu.Lock()
			o.softRecoverCount = 0
			o.softRecoverVerifyUntil = time.Time{}
			o.softRecoverBytesAt = 0
			o.mu.Unlock()
		} else {
			o.mu.Lock()
			o.softRecoverVerifyUntil = time.Time{}
			o.mu.Unlock()
			o.triggerAutoReconnect("Soft без трафика — повторный soft (обновление ключей WG)…")
			return
		}
	}

	if watch && stallDur >= trafficStallThreshold {
		_ = softCount
		o.triggerAutoReconnect(fmt.Sprintf("Трафик не движется %s — soft-восстановление…", stallDur.Round(time.Second)))
	}
}

func (o *Orchestrator) maybeAutoReconnectOnProbeFail() {
	o.mu.Lock()
	watch := o.shouldWatchTraffic(time.Now())
	fails := o.internetProbeFails
	workers := o.lastWorkers
	o.mu.Unlock()
	if !watch || fails < internetProbeFailNeed {
		return
	}
	// 1.1.1.1:443 может не отвечать напрямую при живых TURN-воркерах — не рвём сессию.
	if workers > 0 {
		o.mu.Lock()
		o.internetProbeFails = 0
		o.mu.Unlock()
		return
	}
	o.triggerAutoReconnect(fmt.Sprintf("Нет ответа от интернета (%d проверок) — быстрое восстановление…", fails))
}

func measureInternetRTT() float64 {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", "1.1.1.1:443", internetProbeTimeout)
	if err != nil {
		return 0
	}
	_ = conn.Close()
	return float64(time.Since(start).Milliseconds())
}

func (o *Orchestrator) startInternetPing() {
	o.stopInternetPing()
	stop := make(chan struct{})
	o.pingStop = stop
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(internetProbeInterval):
				ms := measureInternetRTT()
				o.mu.Lock()
				watch := o.shouldWatchTraffic(time.Now())
				if ms > 0 {
					o.internetProbeFails = 0
				} else if watch {
					o.internetProbeFails++
				}
				fails := o.internetProbeFails
				o.mu.Unlock()

				if ms > 0 {
					o.internetMu.Lock()
					o.internetRTTMs = ms
					o.internetMu.Unlock()
				}
				if ms == 0 && fails >= internetProbeFailNeed {
					o.maybeAutoReconnectOnProbeFail()
				}
			}
		}
	}()
}

func (o *Orchestrator) stopInternetPing() {
	if o.pingStop != nil {
		close(o.pingStop)
		o.pingStop = nil
	}
	o.internetMu.Lock()
	o.internetRTTMs = 0
	o.internetMu.Unlock()
}

func (o *Orchestrator) internetRTT() float64 {
	o.internetMu.RLock()
	defer o.internetMu.RUnlock()
	return o.internetRTTMs
}

func (o *Orchestrator) tunnelStatsMeta() (assigned int32, connectedAtMs int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	assigned = o.assignedWorkers
	if !o.connectedAt.IsZero() {
		connectedAtMs = o.connectedAt.UnixMilli()
	}
	return assigned, connectedAtMs
}

func (o *Orchestrator) waitSessionEnd(timeout time.Duration) {
	o.mu.Lock()
	sess := o.sess
	o.mu.Unlock()
	if sess == nil || sess.closed == nil {
		return
	}
	select {
	case <-sess.closed:
	case <-time.After(timeout):
	}
}

func (o *Orchestrator) Start(p ConnectParams) error {
	o.waitSessionEnd(60 * time.Second)

	o.mu.Lock()
	if o.sess != nil {
		o.mu.Unlock()
		return fmt.Errorf("уже подключено")
	}
	o.lastParams = p
	o.assignedWorkers = int32(core.NormalizeWorkers(p.Workers))
	o.connectedAt = time.Time{}
	o.resetWorkersLostState()
	if SoftReconnectPreserve() {
		// SoftReconnect уже переключил VK на direct; не форсируем again через мёртвый WG.
	} else {
		// VK через туннель — всегда включено нативно (тумблер убран из настроек).
		vkThroughTunnel.Store(true)
		o.softRecoverUntil = time.Time{}
		o.softRecoverVKDirect = false
		o.tunnelUp = false
	}
	o.suppressWorkersLost = false
	// Резервируем слот
	placeholder := &coreSession{closed: make(chan struct{})}
	o.sess = placeholder
	o.mu.Unlock()

	sess, err := o.launch(p)
	if err != nil {
		o.mu.Lock()
		if o.sess == placeholder {
			o.sess = nil
		}
		o.mu.Unlock()
		close(placeholder.closed)
		return err
	}

	o.mu.Lock()
	o.sess = sess
	o.mu.Unlock()
	return nil
}

func (o *Orchestrator) launch(p ConnectParams) (*coreSession, error) {
	// Перехватываем стандартный логгер → Wails события
	if _, already := log.Writer().(*wailsLogWriter); !already {
		o.prevLogWriter = log.Writer()
	}
	logName := p.Name
	if logName == "" {
		logName = p.Profile
	}
	lw := &wailsLogWriter{ctx: o.appCtx, file: newSessionLogFile(logName)}
	lw.start()
	log.SetOutput(lw)

	prof, err := loadProfile(p.Profile)
	if err != nil {
		return nil, err
	}

	workers := core.NormalizeWorkers(p.Workers)
	if workers <= 0 {
		workers = 9
	}

	obfsMode := p.ObfsMode
	if obfsMode != "video" {
		obfsMode = "audio"
	}
	tunnelMode := p.TunnelMode
	if tunnelMode != "raw" {
		tunnelMode = "wg"
	}
	turnTransport := p.TurnTransport
	if turnTransport != "udp" {
		turnTransport = "tcp"
	}

	cfg := core.Config{
		PeerAddr:      prof.PeerAddr,
		Password:      prof.Password,
		Hashes:        prof.Hashes,
		Listen:        prof.Listen,
		TurnHost:      prof.TurnHost,
		TurnPort:      prof.TurnPort,
		DeviceID:      prof.DeviceID,
		Workers:       workers,
		CaptchaMode:   p.CaptchaMode,
		MTU:           p.MTU,
		ObfsMode:      obfsMode,
		TunnelMode:    tunnelMode,
		TurnTransport: turnTransport,
		RawPrimaryIP:  "",
		RawDirectPort: prof.RawDirectPort,
	}
	if SoftReconnectPreserve() {
		cfg.TunAlreadyReady = true
		if tunnelMode == "raw" {
			cfg.RawPrimaryIP = ActiveRawPrimaryIP()
		}
	}
	if len(p.Hashes) > 0 {
		cfg.Hashes = p.Hashes
	}

	c := core.New(cfg)
	c.SetOnTurnIPsUpdated(func(ips []string) {
		EnsureTurnDirectRoutes(ips)
	})
	events, err := c.Start()
	if err != nil {
		return nil, fmt.Errorf("core start: %w", err)
	}

	sess := &coreSession{c: c, doneCh: events, closed: make(chan struct{})}
	go func() {
		o.forwardEvents(sess)
		close(sess.closed)
	}()
	return sess, nil
}

func (o *Orchestrator) forwardEvents(sess *coreSession) {
	var connected bool
	for ev := range sess.doneCh {
		switch ev.Type {
		case core.EventState:
			connected = ev.Status == "running"
			runtime.EventsEmit(o.appCtx, "state_changed", ev.Status, "")
			runtime.EventsEmit(o.appCtx, "log", "INFO", fmt.Sprintf("[СОСТОЯНИЕ] %s", ev.Status))
			if !connected && o.onTray != nil {
				o.onTray(false, 0, 0, 0)
			}
		case core.EventStats:
			o.noteTrafficBytes(ev.RxBytes, ev.TxBytes)
			o.noteWorkerStats(ev.Workers)
			if o.onTray != nil {
				o.onTray(connected, ev.RxBytes, ev.TxBytes, ev.Workers)
			}
			assigned, connectedAtMs := o.tunnelStatsMeta()
			runtime.EventsEmit(o.appCtx, "tunnel_stats",
				ev.RxBytes, ev.TxBytes, ev.Workers, assigned, connectedAtMs,
				ev.TurnRTTMs, ev.DTLSHSMs, o.internetRTT(),
			)
		case core.EventLog:
			runtime.EventsEmit(o.appCtx, "log", ev.Level, ev.Message)
			if friendly := formatConnectionError(ev.Message); friendly != "" {
				runtime.EventsEmit(o.appCtx, "error", friendly)
				if strings.Contains(ev.Message, "FATAL_AUTH") {
					go func() {
						if sess.c != nil {
							sess.c.Stop()
						}
					}()
				}
			}
		case core.EventError:
			friendly := formatConnectionError(ev.Message)
			if friendly == "" {
				friendly = ev.Message
			}
			runtime.EventsEmit(o.appCtx, "error", friendly)
			runtime.EventsEmit(o.appCtx, "log", "ERROR", fmt.Sprintf("[ОШИБКА] %s", friendly))
		case core.EventEvent:
			if ev.Name == "wg_config" || ev.Name == "raw_config" {
				turnIPs := sess.c.GetTurnIPs()
				tag := "WG"
				var applyErr error
				if ev.Name == "raw_config" {
					tag = "RAW"
					applyErr = applyRawConfig(ev.Data, turnIPs)
				} else {
					applyErr = applyWGConfig(ev.Data, turnIPs)
				}
				if applyErr != nil {
					msg := fmt.Sprintf("[%s] Ошибка применения конфига: %v", tag, applyErr)
					runtime.EventsEmit(o.appCtx, "error", msg)
					runtime.EventsEmit(o.appCtx, "log", "ERROR", msg)
				} else {
					connected = true
					o.tunnelUp = true
					EnsureTurnDirectRoutes(sess.c.GetTurnIPs())
					sess.c.NotifyTunReady()
					if err := applyVKRouting(); err != nil {
						runtime.EventsEmit(o.appCtx, "log", "WARN", fmt.Sprintf("[%s] VK-маршрутизация: %v", tag, err))
					} else if VKThroughTunnel() {
						runtime.EventsEmit(o.appCtx, "log", "INFO", fmt.Sprintf("[%s] VK идёт через туннель (веб/API), TURN-транспорт напрямую", tag))
					}
					if o.sessionWatchUntil.IsZero() || time.Now().After(o.sessionWatchUntil) {
						o.sessionWatchUntil = time.Now().Add(sessionWatchAfterConnect)
					}
					o.resetWorkersLostState()
					if o.connectedAt.IsZero() {
						o.connectedAt = time.Now()
					}
					if o.pingStop == nil {
						o.startInternetPing()
					}
					if o.trafficWatchStop == nil {
						o.startTrafficWatch()
					}
					if o.netWatchStop == nil {
						o.startNetworkWatch()
					}
					runtime.EventsEmit(o.appCtx, "state_changed", "running", "")
					if SoftReconnectPreserve() && wgTunnelActive() {
						runtime.EventsEmit(o.appCtx, "log", "INFO", fmt.Sprintf("[%s] Soft-reconnect: интерфейс сохранён, воркеры поднимаются", tag))
					} else if tag == "RAW" {
						runtime.EventsEmit(o.appCtx, "log", "INFO", "[RAW] Конфиг применён, туннель активен (без WireGuard) ✓")
					} else {
						runtime.EventsEmit(o.appCtx, "log", "INFO", "[WG] Конфиг применён, туннель активен ✓")
					}
					if o.onTray != nil {
						o.onTray(true, 0, 0, 0)
					}
				}
			}
			runtime.EventsEmit(o.appCtx, "event", ev.Name, ev.Data)
		}
	}
	// Канал закрыт — core завершился
	o.mu.Lock()
	preserve := o.preserveOnSessionEnd
	o.preserveOnSessionEnd = false
	o.mu.Unlock()

	if o.tunnelUp && !o.suppressWorkersLost && !preserve {
		o.emitWorkersLost("Сессия VPN завершилась — нажмите «Переподключить»")
	}
	if !preserve {
		o.tunnelUp = false
		o.suppressWorkersLost = false
		o.stopTrafficWatch()
		o.stopNetworkWatch()
		teardownWG()
		o.stopInternetPing()
		runtime.EventsEmit(o.appCtx, "tunnel_stats", int64(0), int64(0), int32(0), int32(0), int64(0), float64(0), float64(0), float64(0))
	} else {
		runtime.EventsEmit(o.appCtx, "log", "INFO", "[SOFT] VPN-интерфейс сохранён, перезапуск TURN-воркеров…")
		// Soft-сессия умерла сама (не swap SoftReconnect) без воркеров —
		// короткий cool-down, затем можно повторить soft.
		o.mu.Lock()
		if !o.softSwapInProgress && o.softRecoverVKDirect && o.lastWorkers == 0 {
			o.softRecoverVKDirect = false
			o.softRecoverUntil = time.Now().Add(3 * time.Second)
		}
		o.mu.Unlock()
	}
	// Останавливаем буферизованный логгер и восстанавливаем оригинальный
	if lw, ok := log.Writer().(*wailsLogWriter); ok {
		select {
		case <-lw.stop:
		default:
			close(lw.stop)
		}
		if lw.file != nil {
			lw.file.Close()
		}
	}
	if o.prevLogWriter != nil {
		log.SetOutput(o.prevLogWriter)
	}
	ts := time.Now().Format("15:04:05")
	runtime.EventsEmit(o.appCtx, "log", "INFO", fmt.Sprintf("[%s] Сессия завершена", ts))
	if o.onTray != nil && !preserve {
		o.onTray(false, 0, 0, 0)
	}
	o.mu.Lock()
	if o.sess == sess {
		o.sess = nil
	}
	o.mu.Unlock()
	if !preserve {
		runtime.EventsEmit(o.appCtx, "state_changed", "disconnected", "")
	}
}

func (o *Orchestrator) stopCoreSession(fullTeardown bool) {
	o.mu.Lock()
	o.suppressWorkersLost = true
	if fullTeardown {
		o.tunnelUp = false
		o.preserveOnSessionEnd = false
		o.resetWorkersLostState()
		o.stopTrafficWatch()
	} else {
		o.preserveOnSessionEnd = o.tunnelUp && wgTunnelActive()
	}
	sess := o.sess
	o.mu.Unlock()
	if sess == nil || sess.c == nil {
		return
	}
	sess.c.Stop()
	if fullTeardown {
		o.waitSessionEnd(60 * time.Second)
	}
}

func (o *Orchestrator) Stop() {
	SetSoftReconnectPreserve(false)
	o.mu.Lock()
	o.softRecoverUntil = time.Time{}
	o.softRecoverVKDirect = false
	o.softRecoverCount = 0
	o.softRecoverVerifyUntil = time.Time{}
	o.softRecoverBytesAt = 0
	o.mu.Unlock()
	o.stopCoreSession(true)
	o.mu.Lock()
	sess := o.sess
	o.mu.Unlock()
	if sess != nil {
		o.waitSessionEnd(60 * time.Second)
	}
}

func (o *Orchestrator) SendCaptchaResult(token string) {
	o.mu.Lock()
	sess := o.sess
	o.mu.Unlock()
	if sess == nil || sess.c == nil {
		return
	}
	sess.c.SolveCaptcha(token)
}

func (o *Orchestrator) IsRunning() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sess != nil && o.sess.c != nil
}
