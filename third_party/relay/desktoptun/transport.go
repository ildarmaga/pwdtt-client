//go:build linux || windows

package desktoptun

import (
	"context"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"gvisor.dev/gvisor/pkg/tcpip"
)

const (
	maxTCPRelays     = 1024
	maxUDPSessions   = 512
	tcpDialTimeout   = 20 * time.Second
	udpSessionIdle   = 120 * time.Second
)

// directHandler relays captured gVisor flows straight into TunnelDialer (WB
// smux/KCP). Unlike the stock tun2socks tunnel.T() queue it drops overload
// early and never touches SOCKS — same shape as VK vkwg's tnet.DialContext path.
type directHandler struct {
	d      TunnelDialer
	tcpSem chan struct{}
	udpN   atomic.Int32
}

func newDirectHandler(d TunnelDialer) *directHandler {
	return &directHandler{
		d:      d,
		tcpSem: make(chan struct{}, maxTCPRelays),
	}
}

func (h *directHandler) HandleTCP(origin adapter.TCPConn) {
	go h.relayTCP(origin)
}

func (h *directHandler) HandleUDP(origin adapter.UDPConn) {
	if h.udpN.Load() >= maxUDPSessions {
		origin.Close()
		return
	}
	h.udpN.Add(1)
	go h.relayUDP(origin)
}

func (h *directHandler) relayTCP(origin adapter.TCPConn) {
	defer origin.Close()

	id := origin.ID()
	host := tcpipAddrString(id.LocalAddress)
	port := int(id.LocalPort)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if common.IsTunnelSinkHost(addr) || common.IsNoiseDatagramHost(addr) {
		return
	}

	select {
	case h.tcpSem <- struct{}{}:
	case <-time.After(8 * time.Second):
		return
	}
	defer func() { <-h.tcpSem }()

	ctx, cancel := context.WithTimeout(context.Background(), tcpDialTimeout)
	defer cancel()

	remote, err := h.d.DialTCP(ctx, host, port)
	if err != nil {
		return
	}
	defer remote.Close()
	common.EnableTCPKeepAlive(remote, 30*time.Second)
	common.EnableTCPNoDelay(remote)
	relayConn(origin, remote)
}

func (h *directHandler) relayUDP(origin adapter.UDPConn) {
	defer origin.Close()
	defer h.udpN.Add(-1)

	id := origin.ID()
	host := tcpipAddrString(id.LocalAddress)
	port := int(id.LocalPort)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if common.IsTunnelSinkHost(addr) || common.IsNoiseDatagramHost(addr) {
		return
	}

	remote, err := h.d.DialUDP(host, port)
	if err != nil {
		return
	}
	defer remote.Close()

	dst := &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	pipeUDP(origin, remote, dst, udpSessionIdle)
}

func relayConn(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = sessionstats.Copy(b, a, false) }()
	go func() { defer wg.Done(); _, _ = sessionstats.Copy(a, b, true) }()
	wg.Wait()
}

func pipeUDP(origin, remote net.PacketConn, dst *net.UDPAddr, idle time.Duration) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, common.UDPBufSize)
		for {
			_ = origin.SetReadDeadline(time.Now().Add(idle))
			n, _, err := origin.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := remote.WriteTo(buf[:n], dst); err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]byte, common.UDPBufSize)
		for {
			_ = remote.SetReadDeadline(time.Now().Add(idle))
			n, _, err := remote.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := origin.WriteTo(buf[:n], dst); err != nil {
				return
			}
		}
	}()
	wg.Wait()
}

func tcpipAddrString(a tcpip.Address) string {
	b := a.AsSlice()
	if len(b) == 0 {
		return ""
	}
	return net.IP(b).String()
}
