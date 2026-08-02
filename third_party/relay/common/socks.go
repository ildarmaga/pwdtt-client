package common

import (
	"encoding/binary"
	"fmt"
	"net"
)

const SocksLocalhostIP = "127.0.0.1"

// SocksListenAll binds on every interface (needed when a VPN app chains to SOCKS
// from a Network Extension — loopback may not be reachable from the tunnel).
const SocksListenAll = "0.0.0.0"

// IsNonRoutableHost reports addresses that must not be forwarded through the
// creator tunnel (local/private nets and v2ray/xray fake-dns 198.18.0.0/15).
func IsNonRoutableHost(host string) bool {
	hostOnly, _, err := net.SplitHostPort(host)
	if err != nil {
		hostOnly = host
	}
	ip := net.ParseIP(hostOnly)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate()
	}
	if ip4.IsLoopback() || ip4.IsPrivate() || ip4.IsLinkLocalUnicast() || ip4.IsUnspecified() {
		return true
	}
	// v2ray/xray fake DNS pool — CONNECT by IP literal breaks chained SOCKS.
	if ip4[0] == 198 && ip4[1] >= 18 && ip4[1] <= 19 {
		return true
	}
	return false
}

// IsTunnelSinkHost reports destinations that tun2socks/wintun may synthesize on
// Windows (adapter gateway, tunnel subnet). They must not be tunneled or
// local-dialed — that creates a connection storm and exhausts SOCKS ports.
func IsTunnelSinkHost(host string) bool {
	hostOnly, _, err := net.SplitHostPort(host)
	if err != nil {
		hostOnly = host
	}
	ip := net.ParseIP(hostOnly)
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	// wintun gateway noise on Windows (tun2socks CONNECT storms).
	if v4[0] == 172 && v4[1] == 31 && v4[2] == 255 && v4[3] == 254 {
		return true
	}
	// WDTT-WB wintun subnet (10.99.0.x).
	if v4[0] == 10 && v4[1] == 99 && v4[2] == 0 {
		return true
	}
	return false
}

// IsNoiseDatagramHost reports UDP destinations that must never be handled by
// the tun2socks netstack (broadcast, multicast, link-local). Each one otherwise
// spins up a dedicated PacketConn + goroutines and can exhaust memory on a busy LAN.
func IsNoiseDatagramHost(host string) bool {
	hostOnly, _, err := net.SplitHostPort(host)
	if err != nil {
		hostOnly = host
	}
	ip := net.ParseIP(hostOnly)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return true
	}
	if ip4.Equal(net.IPv4bcast) {
		return true
	}
	if ip4[0] >= 224 && ip4[0] <= 239 {
		return true
	}
	if ip4[0] == 169 && ip4[1] == 254 {
		return true
	}
	// WDTT-WB TUN subnet directed broadcast (NetBIOS / game discovery storms).
	if ip4[0] == 10 && ip4[1] == 99 && ip4[3] == 255 {
		return true
	}
	return false
}

const (
	Ver        = 0x05
	CmdTCP     = 0x01
	CmdUDP     = 0x03
	AtypIPv4   = 0x01
	AtypDomain = 0x03
	AtypIPv6   = 0x04

	AuthNone     = 0x00
	AuthUserPass = 0x02
	AuthNoMatch  = 0xFF

	HandshakeBuf = 258
	UDPBufSize   = 4096
	RTPBufSize   = 65536
	// VP8BufSize fits one RTP packet: 1200 MTU - 1 VP8 descriptor - 64 tunnel wrapper - 9 protocol frame
	// (tunnel wrapper = 20 vp8 keepalive header + 4 epoch + 24 XChaCha20 nonce + 16 Poly1305 tag)
	VP8BufSize   = 1126
	DCBufSize    = 32768
)

var (
	NoAuth   = []byte{Ver, AuthNone}
	OK       = []byte{Ver, 0x00, 0x00, AtypIPv4, 0, 0, 0, 0, 0, 0}
	ConnFail = []byte{Ver, 0x05, 0x00, AtypIPv4, 0, 0, 0, 0, 0, 0}
	CmdErr   = []byte{Ver, 0x07, 0x00, AtypIPv4, 0, 0, 0, 0, 0, 0}
	AddrErr  = []byte{Ver, 0x08, 0x00, AtypIPv4, 0, 0, 0, 0, 0, 0}
	GenFail  = []byte{Ver, 0x01, 0x00, AtypIPv4, 0, 0, 0, 0, 0, 0}
)

func NegotiateAuth(conn net.Conn, buf []byte, n int, wantUser, wantPass string) bool {
	if wantUser == "" {
		conn.Write(NoAuth)
		return true
	}
	hasUserPass := false
	methodCount := int(buf[1])
	for i := 0; i < methodCount && i+2 < n; i++ {
		if buf[2+i] == AuthUserPass {
			hasUserPass = true
			break
		}
	}
	if !hasUserPass {
		conn.Write([]byte{Ver, AuthNoMatch})
		return false
	}
	conn.Write([]byte{Ver, AuthUserPass})
	authN, err := conn.Read(buf)
	if err != nil || authN < 5 || buf[0] != 0x01 {
		return false
	}
	userLen := int(buf[1])
	if authN < 2+userLen+1 {
		conn.Write([]byte{0x01, 0x01})
		return false
	}
	user := string(buf[2 : 2+userLen])
	passLen := int(buf[2+userLen])
	if authN < 2+userLen+1+passLen {
		conn.Write([]byte{0x01, 0x01})
		return false
	}
	pass := string(buf[3+userLen : 3+userLen+passLen])
	if user != wantUser || pass != wantPass {
		conn.Write([]byte{0x01, 0x01})
		return false
	}
	conn.Write([]byte{0x01, 0x00})
	return true
}

func ParseAddress(buf []byte, n int) (host string, headerLen int, err error) {
	if n < 7 {
		return "", 0, fmt.Errorf("too short")
	}
	switch buf[3] {
	case AtypIPv4:
		if n < 10 {
			return "", 0, fmt.Errorf("too short for IPv4")
		}
		host = fmt.Sprintf("%d.%d.%d.%d:%d", buf[4], buf[5], buf[6], buf[7],
			binary.BigEndian.Uint16(buf[8:10]))
		return host, 10, nil
	case AtypDomain:
		dlen := int(buf[4])
		if n < 5+dlen+2 {
			return "", 0, fmt.Errorf("too short for domain")
		}
		host = fmt.Sprintf("%s:%d", string(buf[5:5+dlen]),
			binary.BigEndian.Uint16(buf[5+dlen:7+dlen]))
		return host, 5 + dlen + 2, nil
	case AtypIPv6:
		if n < 22 {
			return "", 0, fmt.Errorf("too short for IPv6")
		}
		ip := net.IP(buf[4:20])
		host = fmt.Sprintf("[%s]:%d", ip.String(),
			binary.BigEndian.Uint16(buf[20:22]))
		return host, 22, nil
	default:
		return "", 0, fmt.Errorf("unsupported address type 0x%02x", buf[3])
	}
}
