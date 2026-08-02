// Package vkwg bridges the VK WireGuard+TURN core (vendored from
// wg-turn-client) to a local SOCKS5 proxy via a userspace (netstack)
// WireGuard device. No TUN adapter or admin privileges are required, which
// makes it usable inside the iOS Network Extension / gomobile.
package vkwg

import (
	"context"
	"fmt"
	"math"
	"net"
	"strings"
	"net/netip"
	"sync"
	"sync/atomic"

	vkcore "github.com/ildarmaga/whitelist-bypass/relay/vkcore"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Config configures a VK WireGuard+TURN → SOCKS5 bridge.
type Config struct {
	PeerAddr    string   // WG/DTLS server endpoint, "ip:dtlsPort"
	Password    string   // connection password
	Hashes      []string // VK TURN hashes (parsed from the link)
	DeviceID    string   // stable device identifier
	Workers     int      // TURN workers (0 => core default)
	MTU         int      // 0 => 1380
	CaptchaMode string   // "auto" | "rjs" | "wv"

	UDPListen   string // dispatcher UDP listen addr (default 127.0.0.1:9000)
	SocksListen string // SOCKS5 listen addr (default 127.0.0.1:1080)
	SocksUser   string // optional SOCKS5 username (RFC1929)
	SocksPass   string // optional SOCKS5 password (RFC1929)

	// Log receives structured log lines (level, message). Optional.
	Log func(level, msg string)
}

// Bridge is a running VK WG+TURN → SOCKS5 instance.
type Bridge struct {
	cfg    Config
	core   *vkcore.Core
	dev    *device.Device
	socks  *socksServer
	cancel context.CancelFunc

	mu      sync.Mutex
	started bool

	rxBytes   atomic.Int64
	txBytes   atomic.Int64
	workers   atomic.Int32
	assignedWorkers int32
	turnRTTMs atomic.Uint64 // float64 bits
	dtlsHSMs  atomic.Uint64 // float64 bits
}

// New creates a Bridge. Call Start to run it.
func New(cfg Config) *Bridge {
	if cfg.UDPListen == "" {
		cfg.UDPListen = "127.0.0.1:9000"
	}
	if cfg.SocksListen == "" {
		cfg.SocksListen = "127.0.0.1:1080"
	}
	if cfg.MTU <= 0 {
		cfg.MTU = 1380
	}
	return &Bridge{cfg: cfg}
}

func (b *Bridge) logf(level, format string, args ...interface{}) {
	if b.cfg.Log != nil {
		b.cfg.Log(level, fmt.Sprintf(format, args...))
	}
}

// Start launches the core and, once a WireGuard config is delivered, brings up
// the netstack device and the SOCKS5 server. It returns after the SOCKS5
// listener is bound (or on error). The bridge keeps running until Stop.
func (b *Bridge) Start() error {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return fmt.Errorf("already started")
	}
	b.started = true
	n := b.cfg.Workers
	if n > 108 {
		n = 108
	}
	if n < vkcore.WorkersPerGroup {
		n = vkcore.WorkersPerGroup
	}
	n = (n / vkcore.WorkersPerGroup) * vkcore.WorkersPerGroup
	b.assignedWorkers = int32(n)
	b.mu.Unlock()

	// Bind the SOCKS listener synchronously so callers learn about port
	// conflicts immediately (mirrors the WB joiner's SOCKS gating).
	ln, err := net.Listen("tcp", b.cfg.SocksListen)
	if err != nil {
		return fmt.Errorf("socks listen %s: %w", b.cfg.SocksListen, err)
	}

	c := vkcore.New(vkcore.Config{
		PeerAddr:    b.cfg.PeerAddr,
		Password:    b.cfg.Password,
		Hashes:      b.cfg.Hashes,
		Listen:      b.cfg.UDPListen,
		DeviceID:    b.cfg.DeviceID,
		Workers:     b.cfg.Workers,
		CaptchaMode: b.cfg.CaptchaMode,
		MTU:         b.cfg.MTU,
	})
	events, err := c.Start()
	if err != nil {
		ln.Close()
		return fmt.Errorf("core start: %w", err)
	}
	b.core = c

	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	deviceReady := make(chan error, 1)
	go func() {
		var reported bool
		for ev := range events {
			switch ev.Type {
			case vkcore.EventLog:
				b.logf(ev.Level, "%s", ev.Message)
			case vkcore.EventState:
				b.logf("INFO", "состояние: %s", ev.Status)
			case vkcore.EventError:
				b.logf("ERROR", "%s", ev.Message)
			case vkcore.EventStats:
				b.rxBytes.Store(ev.RxBytes)
				b.txBytes.Store(ev.TxBytes)
				b.workers.Store(ev.Workers)
				b.turnRTTMs.Store(math.Float64bits(ev.TurnRTTMs))
				b.dtlsHSMs.Store(math.Float64bits(ev.DTLSHSMs))
			case vkcore.EventEvent:
				if ev.Name == "wg_config" && !reported {
					reported = true
					if err := b.bringUp(ev.Data, ln); err != nil {
						deviceReady <- err
						return
					}
					deviceReady <- nil
				} else if ev.Name == "captcha_required" {
					b.logf("WARN", "требуется captcha: %s", ev.Data)
				}
			}
		}
		b.logf("WARN", "VK core: все воркеры завершены")
	}()

	select {
	case err := <-deviceReady:
		if err != nil {
			ln.Close()
			c.Stop()
			return err
		}
		return nil
	case <-ctx.Done():
		ln.Close()
		return context.Canceled
	}
}

// bringUp parses the WG config, brings up the netstack device and starts SOCKS5.
func (b *Bridge) bringUp(conf string, ln net.Listener) error {
	p, err := parseWGConfig(conf)
	if err != nil {
		return fmt.Errorf("parse wg config: %w", err)
	}

	mtu := p.mtu
	if mtu <= 0 {
		mtu = b.cfg.MTU
	}

	dns := p.dns
	if len(dns) == 0 {
		dns = []netip.Addr{
			netip.MustParseAddr("1.1.1.1"),
			netip.MustParseAddr("8.8.8.8"),
		}
	}

	tunDev, tnet, err := netstack.CreateNetTUN(p.addresses, dns, mtu)
	if err != nil {
		return fmt.Errorf("create netstack tun: %w", err)
	}

	logger := &device.Logger{
		Verbosef: func(string, ...interface{}) {},
		Errorf:   func(format string, args ...interface{}) { b.logf("WARN", "[WG] "+format, args...) },
	}
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)
	if err := dev.IpcSetOperation(strings.NewReader(p.uapi)); err != nil {
		dev.Close()
		return fmt.Errorf("wg ipc set: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return fmt.Errorf("wg device up: %w", err)
	}
	b.dev = dev

	udpHost := net.IPv4(127, 0, 0, 1)
	if host, _, err := net.SplitHostPort(b.cfg.SocksListen); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			udpHost = ip
		}
	}

	b.socks = &socksServer{
		ln:      ln,
		logf:    func(f string, a ...interface{}) { b.logf("WARN", f, a...) },
		dialTCP: tnet.DialContext,
		dialUDP: func(raddr netip.AddrPort) (net.Conn, error) {
			return tnet.DialUDPAddrPort(netip.AddrPort{}, raddr)
		},
		udpHostIP: udpHost,
		user:      b.cfg.SocksUser,
		pass:      b.cfg.SocksPass,
	}
	go b.socks.serve()

	b.logf("INFO", "VK WG+TURN туннель поднят, SOCKS5 на %s", b.cfg.SocksListen)
	return nil
}

// Stats returns cumulative byte counters and link metrics from the core.
type Stats struct {
	RxBytes         int64
	TxBytes         int64
	Workers         int32
	AssignedWorkers int32
	TurnRTTMs       float64
	DTLSHSMs        float64
}

// Snapshot returns the latest stats reported by the core.
func (b *Bridge) Snapshot() Stats {
	return Stats{
		RxBytes:         b.rxBytes.Load(),
		TxBytes:         b.txBytes.Load(),
		Workers:         b.workers.Load(),
		AssignedWorkers: b.assignedWorkers,
		TurnRTTMs:       math.Float64frombits(b.turnRTTMs.Load()),
		DTLSHSMs:        math.Float64frombits(b.dtlsHSMs.Load()),
	}
}

// Stop tears down the SOCKS server, netstack device and core.
func (b *Bridge) Stop() {
	if b.socks != nil && b.socks.ln != nil {
		b.socks.ln.Close()
	}
	if b.dev != nil {
		b.dev.Close()
		b.dev = nil
	}
	if b.core != nil {
		b.core.Stop()
	}
	if b.cancel != nil {
		b.cancel()
	}
}
