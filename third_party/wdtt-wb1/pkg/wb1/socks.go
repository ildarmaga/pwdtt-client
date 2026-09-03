package wb1

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const (
	socksReadTimeout = 10 * time.Second
	maxSOCKSClients  = 256
	maxUpstreamDials = 64
	copyDrainTimeout = 5 * time.Second
)

// DialFunc opens a TCP stream to dest ("host:port").
type DialFunc func(ctx context.Context, dest string) (net.Conn, error)

const udpDestPrefix = "udp:"

func normalizeMuxDestination(dest string) (hostPort string, udp bool, err error) {
	for {
		stripped, ok := stripUDPDest(dest)
		if !ok {
			break
		}
		udp = true
		dest = stripped
	}
	host, portText, err := net.SplitHostPort(dest)
	if err != nil || strings.TrimSpace(host) == "" {
		return "", false, fmt.Errorf("wb1: invalid destination %q", dest)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", false, fmt.Errorf("wb1: invalid destination port %q", portText)
	}
	return dest, udp, nil
}

func validateMuxDestination(dest string) error {
	_, _, err := normalizeMuxDestination(dest)
	return err
}

// UDPDest marks a mux Open as a datagram flow (SOCKS UDP ASSOCIATE).
func UDPDest(hostPort string) string {
	return udpDestPrefix + hostPort
}

func stripUDPDest(dest string) (string, bool) {
	if strings.HasPrefix(dest, udpDestPrefix) {
		return dest[len(udpDestPrefix):], true
	}
	return dest, false
}

// ServeAccept handles remote Open frames by dialing dest and copying bytes.
// Destinations starting with "udp:" are datagram flows (length-prefixed).
func ServeAccept(ctx context.Context, m *Mux, dial func(dest string) (net.Conn, error)) {
	ServeAcceptUDP(ctx, m, dial, nil)
}

// ServeAcceptUDP is ServeAccept plus a UDP dialer for "udp:" streams.
// If udpDial is nil, UDP dests use net.Dial("udp", host:port) from this process.
func ServeAcceptUDP(ctx context.Context, m *Mux, dial func(dest string) (net.Conn, error), udpDial DialFunc) {
	if udpDial == nil {
		udpDial = func(ctx context.Context, dest string) (net.Conn, error) {
			d := net.Dialer{Timeout: 15 * time.Second}
			return d.DialContext(ctx, "udp", dest)
		}
	}
	dialSlots := make(chan struct{}, maxUpstreamDials)
	for {
		dest, conn, err := m.Accept(ctx)
		if err != nil {
			return
		}
		select {
		case dialSlots <- struct{}{}:
		case <-ctx.Done():
			_ = conn.Close()
			return
		}
		go func(dest string, conn net.Conn) {
			sendErr := func(msg string) {
				if sc, ok := conn.(*streamConn); ok {
					_ = m.send(ctx, Frame{Type: TypeErr, StreamID: sc.id, Payload: []byte(msg)})
				}
				_ = conn.Close()
			}
			hostPort, udp, err := normalizeMuxDestination(dest)
			if err != nil {
				<-dialSlots
				sendErr(err.Error())
				return
			}
			if udp {
				up, err := udpDial(ctx, hostPort)
				<-dialSlots
				if err != nil {
					sendErr(err.Error())
					return
				}
				RelayDatagrams(conn, up)
				return
			}
			up, err := dial(hostPort)
			<-dialSlots
			if err != nil {
				sendErr(err.Error())
				return
			}
			CopyBoth(conn, up)
		}(dest, conn)
	}
}

// DialSOCKS connects to dest through a SOCKS5 proxy.
func DialSOCKS(ctx context.Context, socksAddr, user, pass, dest string) (net.Conn, error) {
	socksAddr = strings.TrimSpace(socksAddr)
	if socksAddr == "" {
		d := net.Dialer{Timeout: 15 * time.Second}
		return d.DialContext(ctx, "tcp", dest)
	}
	var auth *proxy.Auth
	if user != "" {
		auth = &proxy.Auth{User: user, Password: pass}
	}
	base := &net.Dialer{Timeout: 15 * time.Second}
	d, err := proxy.SOCKS5("tcp", socksAddr, auth, base)
	if err != nil {
		return nil, err
	}
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, "tcp", dest)
	}
	return d.Dial("tcp", dest)
}

// ProbeSOCKS tries a TCP connect through the proxy.
func ProbeSOCKS(addr, user, pass string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var last error
	for i := 0; i < 10; i++ {
		c, err := DialSOCKS(ctx, addr, user, pass, "149.154.175.50:443")
		if err == nil {
			_ = c.Close()
			return nil
		}
		last = err
		time.Sleep(2 * time.Second)
	}
	return last
}

// ServeSOCKS is a SOCKS5 listener (CONNECT). UDP ASSOCIATE needs ServeSOCKSUDP.
func ServeSOCKS(ctx context.Context, ln net.Listener, user, pass string, dial DialFunc) error {
	return ServeSOCKSUDP(ctx, ln, user, pass, dial, nil)
}

// ServeSOCKSUDP is SOCKS5 CONNECT + UDP ASSOCIATE (v2rayN TUN / DNS).
func ServeSOCKSUDP(ctx context.Context, ln net.Listener, user, pass string, dial, udpDial DialFunc) error {
	relay := newUDPRelay(ctx, udpDial)
	defer relay.Close()
	clientSlots := make(chan struct{}, maxSOCKSClients)
	for {
		select {
		case clientSlots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		c, err := ln.Accept()
		if err != nil {
			<-clientSlots
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go func(c net.Conn) {
			defer func() { <-clientSlots }()
			if err := socksHandle(ctx, c, user, pass, dial, relay); err != nil {
				_ = c.Close()
			}
		}(c)
	}
}

func socksHandle(ctx context.Context, c net.Conn, user, pass string, dial DialFunc, relay *udpRelay) error {
	_ = c.SetDeadline(time.Now().Add(socksReadTimeout))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return err
	}
	if hdr[0] != 0x05 {
		return fmt.Errorf("socks version %d", hdr[0])
	}
	nmethods := int(hdr[1])
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}
	needAuth := user != ""
	method := byte(0x00)
	if needAuth {
		method = 0x02
	}
	ok := false
	for _, m := range methods {
		if m == method {
			ok = true
			break
		}
	}
	if !ok {
		_, _ = c.Write([]byte{0x05, 0xff})
		return fmt.Errorf("socks: no acceptable method")
	}
	if _, err := c.Write([]byte{0x05, method}); err != nil {
		return err
	}
	if needAuth {
		if err := socksAuth(c, user, pass); err != nil {
			return err
		}
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return err
	}
	if req[0] != 0x05 {
		return fmt.Errorf("socks version %d", req[0])
	}
	if req[1] == 0x03 {
		return socksUDPAssociate(c, req[3], relay)
	}
	if req[1] != 0x01 {
		_, _ = c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return fmt.Errorf("socks: command %d", req[1])
	}
	dest, err := socksReadAddr(c, req[3])
	if err != nil {
		return err
	}
	_ = c.SetDeadline(time.Time{})
	up, err := dial(ctx, dest)
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return err
	}
	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		_ = up.Close()
		return err
	}
	CopyBoth(c, up)
	return nil
}

func socksAuth(c net.Conn, user, pass string) error {
	ver := make([]byte, 2)
	if _, err := io.ReadFull(c, ver); err != nil {
		return err
	}
	if ver[0] != 0x01 {
		return fmt.Errorf("socks auth ver %d", ver[0])
	}
	ulen := int(ver[1])
	ubuf := make([]byte, ulen)
	if _, err := io.ReadFull(c, ubuf); err != nil {
		return err
	}
	plen := make([]byte, 1)
	if _, err := io.ReadFull(c, plen); err != nil {
		return err
	}
	pbuf := make([]byte, int(plen[0]))
	if _, err := io.ReadFull(c, pbuf); err != nil {
		return err
	}
	if string(ubuf) != user || string(pbuf) != pass {
		_, _ = c.Write([]byte{0x01, 0x01})
		return fmt.Errorf("socks auth failed")
	}
	_, err := c.Write([]byte{0x01, 0x00})
	return err
}

func socksReadAddr(c net.Conn, atyp byte) (string, error) {
	var host string
	switch atyp {
	case 0x01:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(c, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return "", err
		}
		name := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, name); err != nil {
			return "", err
		}
		host = string(name)
	case 0x04:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(c, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	default:
		return "", fmt.Errorf("socks atyp %d", atyp)
	}
	portb := make([]byte, 2)
	if _, err := io.ReadFull(c, portb); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portb)
	return net.JoinHostPort(host, fmt.Sprintf("%d", port)), nil
}

// CopyBoth copies both directions and closes both conns.
func CopyBoth(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		closeWrite(a)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		closeWrite(b)
		done <- struct{}{}
	}()
	<-done
	select {
	case <-done:
	case <-time.After(copyDrainTimeout):
	}
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}
