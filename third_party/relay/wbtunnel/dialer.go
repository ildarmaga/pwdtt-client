package wbtunnel

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
)

const maxUDPTunnelConns = 256

// DialTCP opens a tunneled TCP connection to host:port and returns it as a
// net.Conn. It is the in-process equivalent of the SOCKS5 TCP CONNECT path:
// an in-app netstack (tun2socks core) calls this directly instead of dialing a
// loopback SOCKS5 server, so there is no 127.0.0.1:1080 hop to exhaust.
func (j *Joiner) DialTCP(ctx context.Context, host string, port int) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	if common.IsNoiseDatagramHost(addr) {
		return nil, fmt.Errorf("wbt: drop noise %s", addr)
	}
	// v2ray/xray fake-dns must never enter the tunnel.
	if common.IsTunnelSinkHost(addr) {
		return nil, fmt.Errorf("wbt: reject sink %s", addr)
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil && ip[0] == 198 && ip[1] >= 18 && ip[1] <= 19 {
		return nil, fmt.Errorf("wbt: reject fake-dns %s", addr)
	}

	// Local/private targets are dialed directly (LAN, captive portals).
	if common.IsNonRoutableHost(addr) {
		d := net.Dialer{Timeout: 10 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		common.EnableTCPNoDelay(conn)
		return &statsConn{Conn: conn}, nil
	}

	j.mu.Lock()
	sess := j.smuxSess
	j.mu.Unlock()
	if sess == nil {
		return nil, netErrClosed()
	}
	stream, err := sess.OpenStream()
	if err != nil {
		return nil, err
	}
	if err := j.sendConnectRequest(stream, host, port); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("wbt: tunnel %s: %w", common.MaskAddr(addr), err)
	}
	return &streamConn{
		statsConn: statsConn{Conn: stream},
		release:   func() {},
	}, nil
}

// DialUDP returns a net.PacketConn bound to host:port that round-trips
// datagrams through the WB tunnel (pooled smux UDP stream). Used by the
// in-app netstack UDP handler (DNS et al.) instead of the SOCKS UDP relay.
func (j *Joiner) DialUDP(host string, port int) (net.PacketConn, error) {
	if j.udpConns.Load() >= maxUDPTunnelConns {
		return nil, fmt.Errorf("wbt: too many udp handlers")
	}
	dst := &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	if dst.IP == nil {
		return nil, fmt.Errorf("wbt: bad udp host %q", host)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if common.IsNoiseDatagramHost(addr) {
		return nil, fmt.Errorf("wbt: drop noise udp %s", addr)
	}
	j.udpConns.Add(1)
	return &udpTunnelConn{
		j:      j,
		host:   host,
		port:   port,
		dst:    dst,
		closed: make(chan struct{}),
	}, nil
}

// udpTunnelConn is a bidirectional UDP session over one smux stream (games, DNS).
type udpTunnelConn struct {
	j    *Joiner
	host string
	port int
	dst  *net.UDPAddr

	mu        sync.Mutex
	udp       *udpStream
	pendingRx [][]byte
	closed    chan struct{}
	closeOnce sync.Once

	rDeadline time.Time
}

func (c *udpTunnelConn) enqueueRx(payload []byte) {
	if len(payload) == 0 {
		return
	}
	msg := append([]byte(nil), payload...)
	if c.udp != nil {
		select {
		case c.udp.inbound <- msg:
		case <-c.closed:
		default:
		}
		return
	}
	c.pendingRx = append(c.pendingRx, msg)
}

func (c *udpTunnelConn) flushPending() {
	for _, msg := range c.pendingRx {
		select {
		case c.udp.inbound <- msg:
		case <-c.closed:
			return
		default:
		}
	}
	c.pendingRx = nil
}

func (c *udpTunnelConn) openLocked(firstPayload []byte) error {
	j := c.j
	j.mu.Lock()
	sess := j.smuxSess
	j.mu.Unlock()
	if sess == nil {
		return netErrClosed()
	}
	stream, err := sess.OpenStream()
	if err != nil {
		return err
	}
	c.udp = newUDPStream(stream)
	c.flushPending()
	if err := writeUDPRequest(stream, c.host, c.port, firstPayload); err != nil {
		c.udp.Close()
		c.udp = nil
		return err
	}
	return nil
}

func (c *udpTunnelConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	if common.IsTunnelSinkHost(addr) {
		return len(p), nil
	}
	payload := append([]byte(nil), p...)
	if common.IsNonRoutableHost(addr) {
		c.localRoundTrip(payload)
		return len(p), nil
	}
	sessionstats.AddTx(uint64(len(payload)))

	c.mu.Lock()
	if c.udp == nil {
		err := c.openLocked(payload)
		c.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return len(p), nil
	}
	err := writeUDPDatagram(c.udp.stream, payload)
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *udpTunnelConn) localRoundTrip(payload []byte) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(c.host, strconv.Itoa(c.port)), 3*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	sessionstats.AddTx(uint64(len(payload)))
	if _, err := conn.Write(payload); err != nil {
		return
	}
	buf := make([]byte, common.UDPBufSize)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return
	}
	sessionstats.AddRx(uint64(n))
	c.mu.Lock()
	c.enqueueRx(buf[:n])
	c.mu.Unlock()
}

func (c *udpTunnelConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	udp := c.udp
	deadline := c.rDeadline
	c.mu.Unlock()

	var timerC <-chan time.Time
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return 0, nil, timeoutError{}
		}
		t := time.NewTimer(d)
		defer t.Stop()
		timerC = t.C
	}

	if udp == nil {
		if n, ok := c.readPending(p); ok {
			return n, c.dst, nil
		}
		for {
			c.mu.Lock()
			udp = c.udp
			deadline = c.rDeadline
			c.mu.Unlock()
			if udp != nil {
				break
			}
			select {
			case <-c.closed:
				return 0, nil, net.ErrClosed
			default:
			}
			if !deadline.IsZero() && time.Until(deadline) <= 0 {
				return 0, nil, timeoutError{}
			}
			if timerC != nil {
				select {
				case <-timerC:
					return 0, nil, timeoutError{}
				case <-c.closed:
					return 0, nil, net.ErrClosed
				case <-time.After(5 * time.Millisecond):
				}
			} else {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}

	if n, ok := c.readPending(p); ok {
		return n, c.dst, nil
	}

	select {
	case msg := <-udp.inbound:
		if msg == nil {
			return 0, nil, net.ErrClosed
		}
		n := copy(p, msg)
		return n, c.dst, nil
	case <-udp.done:
		return 0, nil, net.ErrClosed
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case <-timerC:
		return 0, nil, timeoutError{}
	}
}

func (c *udpTunnelConn) readPending(p []byte) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pendingRx) == 0 {
		return 0, false
	}
	msg := c.pendingRx[0]
	c.pendingRx = c.pendingRx[1:]
	return copy(p, msg), true
}

func (c *udpTunnelConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.mu.Lock()
		if c.udp != nil {
			c.udp.Close()
			c.udp = nil
		}
		c.mu.Unlock()
		c.j.udpConns.Add(-1)
	})
	return nil
}

func (c *udpTunnelConn) LocalAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func (c *udpTunnelConn) SetDeadline(t time.Time) error {
	_ = c.SetWriteDeadline(t)
	return c.SetReadDeadline(t)
}

func (c *udpTunnelConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	c.mu.Unlock()
	return nil
}

func (c *udpTunnelConn) SetWriteDeadline(time.Time) error { return nil }
