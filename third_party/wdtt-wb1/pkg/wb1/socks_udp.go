package wb1

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	udpFlowMax = 256
)

type udpRelay struct {
	ctx  context.Context
	dial DialFunc

	mu    sync.Mutex
	conn  *net.UDPConn
	port  int
	flows map[string]*udpFlow
}

type udpFlow struct {
	key    string
	dest   string
	header []byte
	stream net.Conn

	mu      sync.Mutex
	replyTo *net.UDPAddr
	last    time.Time
}

func newUDPRelay(ctx context.Context, dial DialFunc) *udpRelay {
	return &udpRelay{ctx: ctx, dial: dial, flows: make(map[string]*udpFlow)}
}

func (r *udpRelay) Close() {
	r.mu.Lock()
	c := r.conn
	r.conn = nil
	flows := r.flows
	r.flows = make(map[string]*udpFlow)
	r.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	for _, f := range flows {
		if f.stream != nil {
			_ = f.stream.Close()
		}
	}
}

func socksUDPAssociate(c net.Conn, atyp byte, relay *udpRelay) error {
	if _, err := socksReadAddr(c, atyp); err != nil {
		return err
	}
	if relay == nil || relay.dial == nil {
		_, _ = c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return fmt.Errorf("socks: udp not enabled")
	}
	port, err := relay.ensure()
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return err
	}
	reply := []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(reply[8:10], uint16(port))
	if _, err := c.Write(reply); err != nil {
		return err
	}
	_ = c.SetDeadline(time.Time{})
	_, _ = io.Copy(io.Discard, c)
	return c.Close()
}

func (r *udpRelay) ensure() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		return r.port, nil
	}
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return 0, err
	}
	r.conn = c
	r.port = c.LocalAddr().(*net.UDPAddr).Port
	go r.loop(c)
	return r.port, nil
}

func (r *udpRelay) loop(c *net.UDPConn) {
	buf := make([]byte, maxDatagram+64)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return
		}
		dest, header, payload, err := parseSocksUDP(buf[:n])
		if err != nil || dest == "" {
			continue
		}
		if skipUDPDest(dest) {
			continue
		}
		if localOnlyDest(dest) {
			relayUDPLocal(c, from, header, dest, payload)
			continue
		}
		f, err := r.flow(c, dest, header, from)
		if err != nil {
			continue
		}
		if err := writeDgram(f.stream, payload); err != nil {
			r.drop(f.key)
		}
	}
}

func (r *udpRelay) flow(c *net.UDPConn, dest string, header []byte, from *net.UDPAddr) (*udpFlow, error) {
	r.mu.Lock()
	if f := r.flows[dest]; f != nil {
		r.mu.Unlock()
		f.mu.Lock()
		f.header = header
		f.replyTo = from
		f.last = time.Now()
		f.mu.Unlock()
		return f, nil
	}
	if len(r.flows) >= udpFlowMax {
		r.mu.Unlock()
		return nil, fmt.Errorf("udp flow table full")
	}
	r.mu.Unlock()

	stream, err := r.dial(r.ctx, UDPDest(dest))
	if err != nil {
		return nil, err
	}
	f := &udpFlow{
		key:     dest,
		dest:    dest,
		header:  header,
		stream:  stream,
		replyTo: from,
		last:    time.Now(),
	}
	r.mu.Lock()
	r.flows[dest] = f
	r.mu.Unlock()
	go r.pump(c, f)
	return f, nil
}

func (r *udpRelay) pump(c *net.UDPConn, f *udpFlow) {
	defer r.drop(f.key)
	for {
		d, err := readDgram(f.stream)
		if err != nil {
			return
		}
		f.mu.Lock()
		hdr := f.header
		to := f.replyTo
		f.last = time.Now()
		f.mu.Unlock()
		if to == nil || len(hdr) == 0 {
			continue
		}
		out := append(append([]byte{}, hdr...), d...)
		_, _ = c.WriteToUDP(out, to)
	}
}

func (r *udpRelay) drop(key string) {
	r.mu.Lock()
	f := r.flows[key]
	delete(r.flows, key)
	r.mu.Unlock()
	if f != nil && f.stream != nil {
		_ = f.stream.Close()
	}
}

func parseSocksUDP(pkt []byte) (dest string, header, payload []byte, err error) {
	if len(pkt) < 10 || pkt[2] != 0 {
		return "", nil, nil, fmt.Errorf("socks udp header")
	}
	atyp := pkt[3]
	off := 4
	var host string
	switch atyp {
	case 0x01:
		if len(pkt) < 10 {
			return "", nil, nil, fmt.Errorf("socks udp ipv4")
		}
		host = net.IP(pkt[4:8]).String()
		off = 8
	case 0x03:
		if len(pkt) < 5 {
			return "", nil, nil, fmt.Errorf("socks udp domain")
		}
		n := int(pkt[4])
		if len(pkt) < 5+n+2 {
			return "", nil, nil, fmt.Errorf("socks udp domain len")
		}
		host = string(pkt[5 : 5+n])
		off = 5 + n
	case 0x04:
		if len(pkt) < 22 {
			return "", nil, nil, fmt.Errorf("socks udp ipv6")
		}
		host = net.IP(pkt[4:20]).String()
		off = 20
	default:
		return "", nil, nil, fmt.Errorf("socks udp atyp %d", atyp)
	}
	port := binary.BigEndian.Uint16(pkt[off : off+2])
	off += 2
	return net.JoinHostPort(host, strconv.Itoa(int(port))), pkt[:off], pkt[off:], nil
}

func encodeSocksUDPIPv4(ip net.IP, port int, payload []byte) []byte {
	ip4 := ip.To4()
	if ip4 == nil {
		ip4 = net.IPv4zero
	}
	out := make([]byte, 10+len(payload))
	out[3] = 0x01
	copy(out[4:8], ip4)
	binary.BigEndian.PutUint16(out[8:10], uint16(port))
	copy(out[10:], payload)
	return out
}

func relayUDPLocal(c *net.UDPConn, from *net.UDPAddr, header []byte, dest string, payload []byte) {
	u, err := net.DialTimeout("udp", dest, 3*time.Second)
	if err != nil {
		return
	}
	defer u.Close()
	if _, err := u.Write(payload); err != nil {
		return
	}
	_ = u.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, maxDatagram)
	n, err := u.Read(buf)
	if err != nil || n == 0 {
		return
	}
	_, _ = c.WriteToUDP(append(append([]byte{}, header...), buf[:n]...), from)
}

func skipUDPDest(dest string) bool {
	host, _, err := net.SplitHostPort(dest)
	if err != nil {
		host = dest
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return ip.IsLoopback() || ip.IsLinkLocalUnicast()
	}
	if v4[0] == 172 && v4[1] == 31 && v4[2] == 255 && v4[3] == 254 {
		return true
	}
	if v4[0] == 10 && v4[1] == 99 && v4[2] == 0 {
		return true
	}
	if v4.Equal(net.IPv4bcast) || (v4[0] >= 224 && v4[0] <= 239) {
		return true
	}
	return false
}

func localOnlyDest(dest string) bool {
	host, _, err := net.SplitHostPort(dest)
	if err != nil {
		host = dest
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	v4 := ip.To4()
	if v4 != nil && v4[0] == 198 && v4[1] >= 18 && v4[1] <= 19 {
		return true
	}
	return false
}
