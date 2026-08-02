package desktoptun

import (
	"context"
	"net"
)

// TunnelDialer routes captured flows straight into an overlay transport
// (e.g. the WB Stream KCP+smux tunnel) instead of a loopback SOCKS5 server.
// When a Config provides one, the in-app netstack dials it directly, removing
// the 127.0.0.1:1080 hop (and the associated port-exhaustion failure mode).
type TunnelDialer interface {
	DialTCP(ctx context.Context, host string, port int) (net.Conn, error)
	DialUDP(host string, port int) (net.PacketConn, error)
}

// lanBypassCIDRs are private (RFC1918) ranges that must never enter the TUN.
//
// Unlike VK's WireGuard path — which encapsulates every captured packet into a
// single UDP stream — the WB tun2socks netstack spins up an independent handler
// (goroutine + dialer) for every 5-tuple it sees. A misbehaving LAN app (e.g. an
// SNMP scanner spraying 10.x.y.z:161) therefore turns into thousands of netstack
// flows per second even though the dialer ultimately local-dials them. Steering
// these ranges to the real gateway BEFORE the split-default routes means LAN
// traffic never reaches wintun at all, matching standard "allow LAN" VPN
// behaviour and keeping the tunnel quiet like VK. The TUN's own subnet keeps its
// more-specific on-link route and is unaffected.
var lanBypassCIDRs = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
}

// publicResolverBypassIPs — public DNS resolvers steered to physical NIC so
// Windows uses router DNS (192.168.x.1) for name resolution (VK WireGuard model).
var publicResolverBypassIPs = []string{
	"1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9",
}

func (t *Tunnel) installPublicResolverBypass() {
	for _, ipStr := range publicResolverBypassIPs {
		if ip := net.ParseIP(ipStr); ip != nil {
			if err := t.AddBypassIP(ip.To4()); err != nil {
				t.log("[desktoptun] resolver bypass %s: %v", ipStr, err)
			}
		}
	}
}
