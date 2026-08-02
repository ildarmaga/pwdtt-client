//go:build linux || windows

package desktoptun

import (
	"fmt"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	t2log "github.com/xjasonlyu/tun2socks/v2/log"
)

// quietTun2socksLogs lowers the tun2socks logger to warn so its per-flow
// "[TCP]/[UDP] a <-> b" info lines (one per connection) don't flood the log.
// The engine path sets this from Key.LogLevel, but the in-process netstack
// path bypasses engine.Start, so without this it defaults to info and a UDP
// burst (e.g. an SNMP scanner on the LAN) prints thousands of lines/sec.
func quietTun2socksLogs() {
	if lg, err := t2log.NewLeveled(t2log.SilentLevel); err == nil {
		t2log.SetLogger(lg)
	}
}

// startTun2socks brings up the wintun adapter and a gVisor netstack wired
// directly to TunnelDialer (VK-style: no loopback SOCKS, no global tunnel.T()).
func (t *Tunnel) startTun2socks() (func(), error) {
	mtu := t.cfg.MTU
	if mtu <= 0 {
		mtu = 1500
	}
	quietTun2socksLogs()

	if t.cfg.Dialer != nil {
		dev, err := tun.Open(t.cfg.AdapterName, uint32(mtu))
		if err != nil {
			return nil, fmt.Errorf("desktoptun: open tun: %w", err)
		}
		handler := newDirectHandler(t.cfg.Dialer)
		st, err := core.CreateStack(&core.Config{
			LinkEndpoint:     dev,
			TransportHandler: handler,
		})
		if err != nil {
			dev.Close()
			return nil, fmt.Errorf("desktoptun: create stack: %w", err)
		}
		quietTun2socksLogs()
		t.log("[desktoptun] direct netstack up (VK-style, no SOCKS) adapter=%s mtu=%d", t.cfg.AdapterName, mtu)
		return func() {
			dev.Close()
			st.Close()
			done := make(chan struct{})
			go func() {
				st.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.log("[desktoptun] netstack wait timeout (dropping active flows)")
			}
		}, nil
	}

	// Legacy SOCKS5 fallback via tun2socks engine.
	return t.startEngineSOCKS(mtu)
}

func (t *Tunnel) startEngineSOCKS(mtu int) (func(), error) {
	proxyURL := fmt.Sprintf("socks5://%s:%d", t.cfg.SocksHost, t.cfg.SocksPort)
	if t.cfg.SocksUser != "" {
		proxyURL = fmt.Sprintf("socks5://%s:%s@%s:%d",
			t.cfg.SocksUser, t.cfg.SocksPass, t.cfg.SocksHost, t.cfg.SocksPort)
	}
	key := &engine.Key{
		Proxy:    proxyURL,
		Device:   "tun://" + t.cfg.AdapterName,
		MTU:      mtu,
		LogLevel: "warn",
	}
	t.log("[desktoptun] starting tun2socks engine adapter=%s mtu=%d proxy=socks5://%s:%d",
		t.cfg.AdapterName, mtu, t.cfg.SocksHost, t.cfg.SocksPort)
	engine.Insert(key)
	engine.Start()
	return func() { engine.Stop() }, nil
}
