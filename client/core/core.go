package core

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config — все параметры запуска (профиль + runtime).
type Config struct {
	PeerAddr    string   // -peer
	Password    string   // -password
	Hashes      []string // -vk (уже распарсенные)
	Listen      string   // -listen, default "127.0.0.1:9000"
	TurnHost    string   // -turn
	TurnPort    string   // -port
	DeviceID    string   // -device-id
	Workers     int      // -n
	CaptchaMode string   // -captcha-mode
	MTU         int      // 0 = default 1380
	ObfsMode       string // audio|video — RTP маскировка (PT 111 / 96)
	TunnelMode     string // wg|raw — raw = IP over DTLS без WireGuard
	TurnTransport  string // tcp|udp — канал клиент↔TURN (default tcp)
	RawPrimaryIP   string // soft-reconnect: IP сохранённого TUN для rewrite
}

// EventType — тип события от ядра.
type EventType string

const (
	EventState EventType = "state"
	EventLog   EventType = "log"
	EventEvent EventType = "event"
	EventError EventType = "error"
	EventStats EventType = "stats"
)

// Event — событие от ядра к orchestrator.
type Event struct {
	Type EventType

	// state
	Status string

	// log
	Level   string
	Message string

	// event
	Name string
	Data string

	// stats
	RxBytes   int64
	TxBytes   int64
	Workers   int32
	TurnRTTMs float64
	DTLSHSMs  float64
}

// Core — runtime controller ядра.
type Core struct {
	cfg               Config
	cancel            context.CancelFunc
	pauseFlag         int32
	CaptchaResultChan chan string
	captchaMode       atomic.Value
	events            chan Event
	once              sync.Once
	turnIPsMu         sync.Mutex
	turnIPs           []string
	tunReady          chan struct{}
	tunReadyOnce      sync.Once
	onTurnIPsUpdated  func([]string)
}

// AddTurnIPs регистрирует TURN IP-адреса (без порта) для исключения из туннеля.
func (c *Core) AddTurnIPs(urls []string) {
	c.turnIPsMu.Lock()
	seen := make(map[string]struct{}, len(c.turnIPs))
	for _, ip := range c.turnIPs {
		seen[ip] = struct{}{}
	}
	changed := false
	for _, u := range urls {
		host := strings.TrimPrefix(u, "turn:")
		host = strings.TrimPrefix(host, "turns:")
		if i := strings.IndexByte(host, '?'); i >= 0 {
			host = host[:i]
		}
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; !ok {
			seen[host] = struct{}{}
			c.turnIPs = append(c.turnIPs, host)
			changed = true
		}
	}
	ips := append([]string(nil), c.turnIPs...)
	cb := c.onTurnIPsUpdated
	c.turnIPsMu.Unlock()
	if changed && cb != nil {
		cb(ips)
	}
}

// SetOnTurnIPsUpdated — callback при новых TURN IP (live exclude routes).
func (c *Core) SetOnTurnIPsUpdated(fn func([]string)) {
	c.turnIPsMu.Lock()
	c.onTurnIPsUpdated = fn
	c.turnIPsMu.Unlock()
}

// NotifyTunReady — TUN/маршруты готовы; остальные воркеры могут dial TURN.
func (c *Core) NotifyTunReady() {
	if c.tunReady == nil {
		return
	}
	c.tunReadyOnce.Do(func() { close(c.tunReady) })
}

// WaitTunReady ждёт NotifyTunReady или timeout.
func (c *Core) WaitTunReady(ctx context.Context, timeout time.Duration) bool {
	if c.tunReady == nil {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.tunReady:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

// GetTurnIPs возвращает все зарегистрированные TURN IP.
func (c *Core) GetTurnIPs() []string {
	c.turnIPsMu.Lock()
	defer c.turnIPsMu.Unlock()
	result := make([]string, len(c.turnIPs))
	copy(result, c.turnIPs)
	return result
}

// New создаёт Core. Start() запускает его.
func New(cfg Config) *Core {
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:9000"
	}
	if cfg.DeviceID == "" {
		cfg.DeviceID = "unknown"
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 9
	}
	c := &Core{
		cfg:               cfg,
		CaptchaResultChan: make(chan string, 1),
		events:            make(chan Event, 256),
		tunReady:          make(chan struct{}),
	}
	c.captchaMode.Store(normalizeCaptchaMode(cfg.CaptchaMode))
	return c
}

// Start запускает ядро. Возвращает канал событий (закрывается при завершении).
func (c *Core) Start() (<-chan Event, error) {
	setupGlobalResolver()

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	if c.cfg.PeerAddr == "" {
		cancel()
		return nil, fmt.Errorf("PeerAddr is required")
	}
	if len(c.cfg.Hashes) == 0 {
		cancel()
		return nil, fmt.Errorf("Hashes are required")
	}
	if c.cfg.Password == "" {
		cancel()
		return nil, fmt.Errorf("Password is required")
	}

	peer, err := net.ResolveUDPAddr("udp", c.cfg.PeerAddr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("resolve peer: %w", err)
	}

	wrapKey, err := deriveWrapKey(c.cfg.Password)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("derive wrap key: %w", err)
	}

	tunnelMode := c.cfg.TunnelMode
	if tunnelMode != "raw" {
		tunnelMode = "wg"
	}

	n := NormalizeWorkers(c.cfg.Workers)
	mtu := c.cfg.MTU
	if mtu <= 0 {
		mtu = 1280
	}

	turnTransport := c.cfg.TurnTransport
	if turnTransport != "udp" {
		turnTransport = "tcp"
	}

	tp := &TurnParams{
		Host:          c.cfg.TurnHost,
		Port:          c.cfg.TurnPort,
		Hashes:        c.cfg.Hashes,
		WrapKey:       wrapKey,
		ObfsMode:      c.cfg.ObfsMode,
		TunnelMode:    tunnelMode,
		TurnTransport: turnTransport,
		MTU:           mtu,
		RawPrimaryIP:  c.cfg.RawPrimaryIP,
	}

	localConn, err := net.ListenPacket("udp", c.cfg.Listen)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("listen %s: %w", c.cfg.Listen, err)
	}
	if uc, ok := localConn.(*net.UDPConn); ok {
		_ = uc.SetReadBuffer(socketBufSize)
		_ = uc.SetWriteBuffer(socketBufSize)
	}

	_, localPort, _ := net.SplitHostPort(c.cfg.Listen)
	if localPort == "" {
		localPort = "9000"
	}

	numGroups := n / workersPerGroup
	perGroup := workersPerGroup

	stats := NewStats()
	emitCaptchaRequest := func(mode, redirectURI, sessionToken string) {
		c.emit(Event{Type: EventEvent, Name: "captcha_required", Data: mode + "|" + redirectURI + "|" + sessionToken})
	}

	shutdownCh := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(shutdownCh)
	}()
	go stats.RunLoop(shutdownCh,
		func(level, msg string) {
			c.emit(Event{Type: EventLog, Level: level, Message: msg})
		},
		func(rx, tx int64, workers int32, turnRttMs, dtlsHsMs float64) {
			c.emit(Event{Type: EventStats, RxBytes: rx, TxBytes: tx, Workers: workers, TurnRTTMs: turnRttMs, DTLSHSMs: dtlsHsMs})
		},
	)

	// RAW: multipath RR + RA-frame reorder (не sticky). Sticky оставлял 1 TURN ≈ 1–2 Мбит.
	disp := NewDispatcher(ctx, localConn, stats, tunnelMode == "raw")

	configCh := make(chan string, 1)
	// RAW: один gate на все группы — иначе группа #2 без configCh → nil gate →
	// воркеры без RAWCONF идут как WG-прокси, sticky шлёт туда TCP → трафик мёртв.
	var sharedRawGate *wgConfigGate
	if tunnelMode == "raw" {
		sharedRawGate = newWGConfigGate(configCh, "raw", mtu, c.cfg.RawPrimaryIP)
	}

	go func() {
		select {
		case rawConf, ok := <-configCh:
			if !ok || rawConf == "" {
				return
			}
			if tunnelMode == "raw" {
				c.emit(Event{Type: EventEvent, Name: "raw_config", Data: rawConf})
				return
			}
			finalConf := patchWGConfig(rawConf, c.cfg.MTU)
			c.emit(Event{Type: EventEvent, Name: "wg_config", Data: finalConf})
		case <-ctx.Done():
		}
	}()

	c.emit(Event{Type: EventState, Status: "connecting"})

	go func() {
		defer close(c.events)
		defer disp.Shutdown()
		defer cancel()
		defer func() { _ = localConn.Close() }()

		var wg sync.WaitGroup
		workerIDCounter := 1
		var prevWaitReady <-chan struct{}

		for g := 0; g < numGroups; g++ {
			isFirst := g == 0
			var myWaitReady <-chan struct{}
			var mySignalReady chan<- struct{}

			if g > 0 {
				myWaitReady = prevWaitReady
			}
			if g < numGroups-1 {
				ch := make(chan struct{})
				mySignalReady = ch
				prevWaitReady = ch
			}

			ids := make([]int, perGroup)
			for i := range ids {
				ids[i] = workerIDCounter
				workerIDCounter++
			}

			gID := g + 1
			var cc chan<- string
			if isFirst {
				cc = configCh
			}

			waitTun := func(ctx context.Context) bool {
				ok := c.WaitTunReady(ctx, 20*time.Second)
				if !ok {
					log.Printf("[ГРУППА] TUN не готов за 20s — воркер не стартует")
				}
				return ok
			}
			wg.Add(1)
			go func(groupID int, isFirstGroup bool, configChan chan<- string, workerIds []int, startHashIndex int, waitR <-chan struct{}, sigR chan<- struct{}) {
				defer wg.Done()
				WorkerGroup(ctx, groupID, startHashIndex, tp, peer, disp, localPort,
					isFirstGroup, configChan, sharedRawGate, workerIds, &c.pauseFlag,
					c.cfg.DeviceID, c.cfg.Password, stats, waitR, sigR,
					c.CaptchaResultChan, c.getCaptchaMode, emitCaptchaRequest, c.AddTurnIPs, waitTun)
			}(gID, isFirst, cc, ids, g, myWaitReady, mySignalReady)
		}

		wg.Wait()
		close(configCh)
		log.Println("[CORE] все воркеры завершены")
	}()

	return c.events, nil
}

// Stop останавливает ядро.
func (c *Core) Stop() {
	c.once.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
	})
}

// Pause приостанавливает воркеры.
func (c *Core) Pause() { atomic.StoreInt32(&c.pauseFlag, 1) }

// Resume возобновляет воркеры.
func (c *Core) Resume() { atomic.StoreInt32(&c.pauseFlag, 0) }

// SolveCaptcha передаёт токен капчи в ядро.
func (c *Core) SolveCaptcha(token string) {
	// Дренируем устаревший результат
	select {
	case <-c.CaptchaResultChan:
	default:
	}
	c.CaptchaResultChan <- token
}

func (c *Core) emit(ev Event) {
	select {
	case c.events <- ev:
	default:
		if ev.Type != EventLog {
			// Drain one stale entry to make room for important event
			select {
			case <-c.events:
			default:
			}
			select {
			case c.events <- ev:
			default:
			}
		}
	}
}

func (c *Core) getCaptchaMode() string {
	mode, _ := c.captchaMode.Load().(string)
	if mode == "" {
		return "auto"
	}
	return mode
}

func normalizeCaptchaMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto", "rjs", "wv":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "auto"
	}
}
