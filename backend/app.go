package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	orch          *Orchestrator
	wb            *WBManager
	trayEnabled   atomic.Bool
	quitting      atomic.Bool
	showOnStartup bool
	trayIcon      []byte

	updateMu       sync.Mutex
	updateProgress UpdateProgress
	updateActive   bool
}

func NewApp(trayIcon []byte) *App { return &App{trayIcon: trayIcon} }

func (a *App) SetShowOnStartup(v bool) { a.showOnStartup = v }

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.orch = NewOrchestrator(ctx, a.updateTray)
	a.wb = NewWBManager(ctx)
	startTray(a.trayIcon,
		func() { runtime.WindowShow(ctx) },
		func() {
			if a.orch.IsRunning() || a.wb.IsRunning() {
				a.orch.Stop()
				a.wb.Disconnect()
			} else {
				runtime.WindowShow(ctx)
			}
		},
		func() { a.quitting.Store(true); a.orch.Stop(); a.wb.Disconnect(); os.Exit(0) },
	)
	if a.showOnStartup {
		go func() {
			time.Sleep(200 * time.Millisecond)
			runtime.WindowUnminimise(ctx)
			runtime.WindowShow(ctx)
			runtime.WindowCenter(ctx)
			bringMainWindowToFront()
		}()
	}
}

func (a *App) updateTray(connected bool, rx, tx int64, workers int32) {
	setTrayStatus(connected, rx, tx, workers)
}

// OnBeforeClose hides the window instead of quitting when tray is enabled.
func (a *App) OnBeforeClose(ctx context.Context) bool {
	if a.trayEnabled.Load() && !a.quitting.Load() {
		runtime.WindowHide(ctx)
		return true // prevent close
	}
	return false
}

func (a *App) Connect(p ConnectParams) error {
	// Ensure WB is fully stopped before bringing up the VK tunnel.
	// Without this, switching protocol in Settings while WB is connected
	// leaves WB alive in the same room alongside the new VK session.
	if a.wb.IsRunning() {
		a.wb.Disconnect()
	}
	return a.orch.Start(p)
}
func (a *App) Disconnect() {
	a.orch.Stop()
	a.schedulePendingUpdateApply()
}
func (a *App) Reconnect() error { return a.orch.Reconnect() }
func (a *App) IsRunning() bool { return a.orch.IsRunning() || a.wb.IsRunning() }

// IsWBRunning reports whether the WB Stream tunnel is active (for UI sync).
func (a *App) IsWBRunning() bool { return a.wb.IsRunning() }

// ConnectWB поднимает WB Stream в режиме SOCKS-only (как iOS → V2BOX). socksOnly игнорируется (всегда true).
func (a *App) ConnectWB(room string, routingPayload string, vp8Fps, vp8Batch int, dualTrack bool, socksOnly bool, socksPort int, socksUser, socksPass string) error {
	// Ensure the VK/orch tunnel is stopped before starting WB so the two
	// don't coexist and fight over routes / produce a peer-restart storm.
	if a.orch.IsRunning() {
		a.orch.Stop()
	}
	return a.wb.Connect(room, routingPayload, vp8Fps, vp8Batch, dualTrack, true, socksPort, socksUser, socksPass)
}

// GetWBSocksEndpoint returns local SOCKS5 details when WB is in socks-only mode.
func (a *App) GetWBSocksEndpoint() map[string]interface{} {
	host, port, user, pass, ok := a.wb.SocksEndpoint()
	if !ok {
		return map[string]interface{}{"ok": false}
	}
	// iOS-compatible share URL: socks://BASE64(user:pass)@host:port
	url := fmt.Sprintf("socks://%s:%d", host, port)
	if user != "" {
		creds := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		url = fmt.Sprintf("socks://%s@%s:%d", creds, host, port)
	}
	return map[string]interface{}{
		"ok":   true,
		"host": host,
		"port": port,
		"user": user,
		"pass": pass,
		"url":  url,
	}
}

// DisconnectWB останавливает WB Stream туннель.
func (a *App) DisconnectWB() {
	a.wb.Disconnect()
	a.schedulePendingUpdateApply()
}

// SetVKThroughTunnel переключает маршрутизацию VK (веб/API) через туннель на лету.
// Применяется немедленно, если туннель активен; иначе — при следующем подключении.
func (a *App) SetVKThroughTunnel(through bool) error { return a.orch.SetVKThroughTunnel(through) }

// GetVKThroughTunnel возвращает текущий режим маршрутизации VK.
func (a *App) GetVKThroughTunnel() bool { return VKThroughTunnel() }

// CheckVPN returns names of active VPN interfaces (excluding our wg-turn).
func (a *App) CheckVPN() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var found []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		n := strings.ToLower(iface.Name)
		if n == wgIface {
			continue
		}
		if strings.HasPrefix(n, "tun") ||
			strings.HasPrefix(n, "tap") ||
			strings.HasPrefix(n, "wg") ||
			strings.HasPrefix(n, "ppp") ||
			strings.HasPrefix(n, "nordlynx") ||
			strings.HasPrefix(n, "proton") ||
			strings.HasPrefix(n, "utun") ||
			strings.HasPrefix(n, "ipsec") {
			found = append(found, iface.Name)
		}
	}
	return found
}

func (a *App) SaveProfile(name string, p ProfileData) error {
	dir := filepath.Join(configDir(), "profiles")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if p.DeviceID == "" {
		if existing, err := loadProfile(name); err == nil && existing.DeviceID != "" {
			p.DeviceID = existing.DeviceID
		} else {
			p.DeviceID = uuid.New().String()
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(profilePath(name), data, 0600)
}

func (a *App) GetProfile(name string) (*ProfileData, error) {
	return loadProfile(name)
}

func (a *App) DeleteProfile(name string) error {
	return os.Remove(profilePath(name))
}
