package vkwg

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// tcpDialer dials a TCP connection inside the WireGuard netstack.
type tcpDialer func(ctx context.Context, network, address string) (net.Conn, error)

// udpDialer opens a UDP socket inside the WireGuard netstack toward raddr.
type udpDialer func(raddr netip.AddrPort) (net.Conn, error)

// socksServer is a minimal SOCKS5 server that routes CONNECT and UDP ASSOCIATE
// traffic through the supplied netstack dialers. When user/pass are set it
// requires RFC1929 auth; otherwise it accepts no-auth.
type socksServer struct {
	ln        net.Listener
	dialTCP   tcpDialer
	dialUDP   udpDialer
	logf      func(string, ...interface{})
	udpHostIP net.IP // IP that UDP-associate sockets bind to
	user      string
	pass      string
}

func (s *socksServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *socksServer) handle(c net.Conn) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(15 * time.Second))

	// greeting: VER NMETHODS METHODS...
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 0x05 {
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	if !s.negotiateAuth(c, methods) {
		return
	}

	// request: VER CMD RSV ATYP ...
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[0] != 0x05 {
		return
	}
	cmd := req[1]
	host, _, err := readAddr(c, req[3])
	if err != nil {
		s.reply(c, 0x01, nil)
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(c, portBuf); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBuf)
	c.SetReadDeadline(time.Time{})

	switch cmd {
	case 0x01: // CONNECT
		s.handleConnect(c, host, port)
	case 0x03: // UDP ASSOCIATE
		s.handleUDPAssociate(c)
	default:
		s.reply(c, 0x07, nil) // command not supported
	}
}

// negotiateAuth performs SOCKS5 method selection and, when configured, the
// RFC1929 username/password sub-negotiation. Returns false on failure.
func (s *socksServer) negotiateAuth(c net.Conn, methods []byte) bool {
	has := func(m byte) bool {
		for _, x := range methods {
			if x == m {
				return true
			}
		}
		return false
	}

	requireAuth := s.user != "" || s.pass != ""

	// Prefer no-auth when allowed and offered; otherwise fall back to user/pass.
	if !requireAuth && has(0x00) {
		_, err := c.Write([]byte{0x05, 0x00})
		return err == nil
	}
	if has(0x02) {
		if _, err := c.Write([]byte{0x05, 0x02}); err != nil {
			return false
		}
		return s.readUserPass(c)
	}
	// No acceptable method.
	c.Write([]byte{0x05, 0xFF})
	return false
}

func (s *socksServer) readUserPass(c net.Conn) bool {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 0x01 {
		return false
	}
	ulen := int(hdr[1])
	ubuf := make([]byte, ulen+1)
	if _, err := io.ReadFull(c, ubuf); err != nil {
		return false
	}
	user := string(ubuf[:ulen])
	plen := int(ubuf[ulen])
	pbuf := make([]byte, plen)
	if _, err := io.ReadFull(c, pbuf); err != nil {
		return false
	}
	pass := string(pbuf)

	ok := true
	if s.user != "" || s.pass != "" {
		ok = user == s.user && pass == s.pass
	}
	status := byte(0x00)
	if !ok {
		status = 0x01
	}
	if _, err := c.Write([]byte{0x01, status}); err != nil {
		return false
	}
	return ok
}

func (s *socksServer) handleConnect(c net.Conn, host string, port uint16) {
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	remote, err := s.dialTCP(ctx, "tcp", target)
	if err != nil {
		if s.logf != nil {
			s.logf("socks: dial %s failed: %v", target, err)
		}
		s.reply(c, 0x05, nil) // connection refused
		return
	}
	defer remote.Close()
	s.reply(c, 0x00, nil)
	pipe(c, remote)
}

// handleUDPAssociate binds a host UDP relay socket, tells the client its
// address, then relays datagrams between the client and the netstack.
func (s *socksServer) handleUDPAssociate(c net.Conn) {
	hostIP := s.udpHostIP
	if hostIP == nil {
		hostIP = net.IPv4(127, 0, 0, 1)
	}
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: hostIP, Port: 0})
	if err != nil {
		s.reply(c, 0x01, nil)
		return
	}
	defer relay.Close()

	bound := relay.LocalAddr().(*net.UDPAddr)
	s.reply(c, 0x00, bound)

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		for {
			// The TCP control connection staying open keeps the association
			// alive; any read result (data or EOF) tears it down.
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()

	relayUDP(relay, s.dialUDP, s.logf, done)
}

// udpSession tracks one client<->netstack UDP flow.
type udpSession struct {
	conn net.Conn
}

func relayUDP(relay *net.UDPConn, dial udpDialer, logf func(string, ...interface{}), done <-chan struct{}) {
	var (
		mu       sync.Mutex
		sessions = map[string]*udpSession{} // keyed by "clientAddr|dstAddr"
		client   *net.UDPAddr
	)
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-done:
			mu.Lock()
			for _, sess := range sessions {
				sess.conn.Close()
			}
			mu.Unlock()
			return
		default:
		}
		relay.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, caddr, err := relay.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		client = caddr
		dstHost, dstPort, payloadOff, perr := parseUDPHeader(buf[:n])
		if perr != nil {
			continue
		}
		ap, perr := resolveAddrPort(dstHost, dstPort)
		if perr != nil {
			continue
		}
		key := caddr.String() + "|" + ap.String()
		mu.Lock()
		sess := sessions[key]
		if sess == nil {
			rc, derr := dial(ap)
			if derr != nil {
				mu.Unlock()
				if logf != nil {
					logf("socks-udp: dial %s failed: %v", ap, derr)
				}
				continue
			}
			sess = &udpSession{conn: rc}
			sessions[key] = sess
			hdr := udpReplyHeader(ap)
			go func(rc net.Conn, cl *net.UDPAddr, hdr []byte) {
				rbuf := make([]byte, 64*1024)
				for {
					rc.SetReadDeadline(time.Now().Add(60 * time.Second))
					rn, rerr := rc.Read(rbuf)
					if rerr != nil {
						return
					}
					out := append(append([]byte{}, hdr...), rbuf[:rn]...)
					relay.WriteToUDP(out, cl)
				}
			}(rc, caddr, hdr)
		}
		mu.Unlock()
		sess.conn.Write(buf[payloadOff:n])
	}
	_ = client
}

// parseUDPHeader parses the SOCKS5 UDP request header and returns dst host/port
// and the offset of the payload.
func parseUDPHeader(b []byte) (host string, port uint16, off int, err error) {
	if len(b) < 4 {
		return "", 0, 0, fmt.Errorf("short udp header")
	}
	if b[2] != 0x00 {
		return "", 0, 0, fmt.Errorf("fragmentation unsupported")
	}
	atyp := b[3]
	p := 4
	switch atyp {
	case 0x01:
		if len(b) < p+4+2 {
			return "", 0, 0, fmt.Errorf("short ipv4")
		}
		host = net.IP(b[p : p+4]).String()
		p += 4
	case 0x04:
		if len(b) < p+16+2 {
			return "", 0, 0, fmt.Errorf("short ipv6")
		}
		host = net.IP(b[p : p+16]).String()
		p += 16
	case 0x03:
		if len(b) < p+1 {
			return "", 0, 0, fmt.Errorf("short domain len")
		}
		l := int(b[p])
		p++
		if len(b) < p+l+2 {
			return "", 0, 0, fmt.Errorf("short domain")
		}
		host = string(b[p : p+l])
		p += l
	default:
		return "", 0, 0, fmt.Errorf("bad atyp")
	}
	port = binary.BigEndian.Uint16(b[p : p+2])
	p += 2
	return host, port, p, nil
}

// udpReplyHeader builds the SOCKS5 UDP reply header for datagrams sent back to
// the client (RSV=0, FRAG=0, ATYP+addr+port).
func udpReplyHeader(ap netip.AddrPort) []byte {
	h := []byte{0, 0, 0}
	ip := ap.Addr()
	if ip.Is4() {
		h = append(h, 0x01)
		b := ip.As4()
		h = append(h, b[:]...)
	} else {
		h = append(h, 0x04)
		b := ip.As16()
		h = append(h, b[:]...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], ap.Port())
	return append(h, pb[:]...)
}

func resolveAddrPort(host string, port uint16) (netip.AddrPort, error) {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		// Domain names are resolved by tun2socks before reaching us; if a raw
		// domain arrives we cannot resolve it here without netstack DNS.
		ips, lerr := net.LookupIP(host)
		if lerr != nil || len(ips) == 0 {
			return netip.AddrPort{}, fmt.Errorf("resolve %s: %v", host, lerr)
		}
		ip, _ = netip.AddrFromSlice(ips[0])
	}
	return netip.AddrPortFrom(ip.Unmap(), port), nil
}

// readAddr reads a SOCKS5 address of the given ATYP from r.
func readAddr(r io.Reader, atyp byte) (host string, n int, err error) {
	switch atyp {
	case 0x01:
		b := make([]byte, 4)
		if _, err = io.ReadFull(r, b); err != nil {
			return
		}
		return net.IP(b).String(), 4, nil
	case 0x04:
		b := make([]byte, 16)
		if _, err = io.ReadFull(r, b); err != nil {
			return
		}
		return net.IP(b).String(), 16, nil
	case 0x03:
		l := make([]byte, 1)
		if _, err = io.ReadFull(r, l); err != nil {
			return
		}
		b := make([]byte, int(l[0]))
		if _, err = io.ReadFull(r, b); err != nil {
			return
		}
		return string(b), int(l[0]) + 1, nil
	default:
		return "", 0, fmt.Errorf("bad atyp %d", atyp)
	}
}

func (s *socksServer) reply(c net.Conn, code byte, bind *net.UDPAddr) {
	resp := []byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if bind != nil {
		ip := bind.IP.To4()
		if ip != nil {
			copy(resp[4:8], ip)
		}
		binary.BigEndian.PutUint16(resp[8:10], uint16(bind.Port))
	}
	c.Write(resp)
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
}
