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
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.Getenv("HOME")
	}
	dir := filepath.Join(base, "pwdtt")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func profilePath(name string) string {
	return filepath.Join(configDir(), "profiles", name+".json")
}

// ProfileData — хранится в ~/.config/pwdtt/profiles/<name>.json
type ProfileData struct {
	PeerAddr string   `json:"peer"`
	Password string   `json:"password"`
	Hashes   []string `json:"hashes"`
	Listen   string   `json:"listen,omitempty"`
	TurnHost string   `json:"turn,omitempty"`
	TurnPort string   `json:"port,omitempty"`
	DeviceID string   `json:"device_id,omitempty"`
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
	preserveOnSessionEnd bool
	sessionWatchUntil    time.Time
	netWatchStop         chan struct{}
}

const networkChangeDebounce = 1500 * time.Millisecond

const workersLostGrace = 4 * time.Second

const (
	trafficStallThreshold   = 8 * time.Second  // залипание пути (игры не терпят 20–30 с)
	trafficActiveMinBytes   = int64(512)       // мелкие игровые пакеты
	trafficActiveWindow     = 3 * time.Minute
	sessionWatchAfterConnect = 3 * time.Minute // после Connect всегда следим за залипанием
	autoReconnectCooldown   = 10 * time.Second
	autoReconnectProbeEvery = 1 * time.Second
	internetProbeInterval   = 2 * time.Second
	internetProbeTimeout    = 1500 * time.Millisecond
	internetProbeFailNeed   = 2
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
	o.mu.Unlock()
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
	o.mu.Unlock()
	if params.Profile == "" {
		return fmt.Errorf("нет сохранённых параметров подключения")
	}
	if canPreserve {
		SetSoftReconnectPreserve(true)
		defer SetSoftReconnectPreserve(false)
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
	o.mu.Unlock()
	return o.Start(params)
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
	o.mu.Unlock()
	if !o.tunnelUp {
		return
	}
	if workers > 0 {
		o.workersZeroAt = time.Time{}
		o.workersLostAt = false
		return
	}
	if o.workersZeroAt.IsZero() {
		o.workersZeroAt = time.Now()
		return
	}
	if time.Since(o.workersZeroAt) >= workersLostGrace {
		o.triggerAutoReconnect("Нет активных воркеров — быстрое восстановление…")
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
	if !o.lastAutoReconnectAt.IsZero() && time.Since(o.lastAutoReconnectAt) < autoReconnectCooldown {
		o.mu.Unlock()
		return
	}
	o.autoReconnecting = true
	o.lastAutoReconnectAt = time.Now()
	soft := !forceFull && o.tunnelUp && wgTunnelActive()
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
		if soft {
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
	o.mu.Unlock()

	if watch && stallDur >= trafficStallThreshold {
		o.triggerAutoReconnect(fmt.Sprintf("Трафик не движется %s — быстрое восстановление…", stallDur.Round(time.Second)))
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
	// VK через туннель — всегда включено нативно (тумблер убран из настроек).
	vkThroughTunnel.Store(true)
	o.resetWorkersLostState()
	if !SoftReconnectPreserve() {
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

	workers := p.Workers
	if workers <= 0 {
		workers = 9
	}

	cfg := core.Config{
		PeerAddr:    prof.PeerAddr,
		Password:    prof.Password,
		Hashes:      prof.Hashes,
		Listen:      prof.Listen,
		TurnHost:    prof.TurnHost,
		TurnPort:    prof.TurnPort,
		DeviceID:    prof.DeviceID,
		Workers:     workers,
		CaptchaMode: p.CaptchaMode,
		MTU:         p.MTU,
	}
	if len(p.Hashes) > 0 {
		cfg.Hashes = p.Hashes
	}

	c := core.New(cfg)
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
			runtime.EventsEmit(o.appCtx, "tunnel_stats",
				ev.RxBytes, ev.TxBytes, ev.Workers,
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
			if ev.Name == "wg_config" {
				turnIPs := sess.c.GetTurnIPs()
				if err := applyWGConfig(ev.Data, turnIPs); err != nil {
					msg := fmt.Sprintf("[WG] Ошибка применения конфига: %v", err)
					runtime.EventsEmit(o.appCtx, "error", msg)
					runtime.EventsEmit(o.appCtx, "log", "ERROR", msg)
				} else {
					connected = true
					o.tunnelUp = true
					if err := applyVKRouting(); err != nil {
						runtime.EventsEmit(o.appCtx, "log", "WARN", fmt.Sprintf("[WG] VK-маршрутизация: %v", err))
					} else if VKThroughTunnel() {
						runtime.EventsEmit(o.appCtx, "log", "INFO", "[WG] VK идёт через туннель (веб/API), TURN-транспорт напрямую")
					}
					if o.sessionWatchUntil.IsZero() || time.Now().After(o.sessionWatchUntil) {
						o.sessionWatchUntil = time.Now().Add(sessionWatchAfterConnect)
					}
					o.resetWorkersLostState()
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
						runtime.EventsEmit(o.appCtx, "log", "INFO", "[WG] Soft-reconnect: wg-turn сохранён, воркеры поднимаются")
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
		runtime.EventsEmit(o.appCtx, "tunnel_stats", int64(0), int64(0), int32(0), float64(0), float64(0), float64(0))
	} else {
		runtime.EventsEmit(o.appCtx, "log", "INFO", "[SOFT] VPN-интерфейс сохранён, перезапуск TURN-воркеров…")
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
