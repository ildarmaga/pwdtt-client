package wbtunnel

import (
	"encoding/binary"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
)

const socksUDPFlowIdleTTL = 90 * time.Second
const socksUDPFlowMax = 256

// socksUDPFlow is a bidirectional UDP session for one destination behind the
// shared SOCKS5 UDP ASSOCIATE relay. The old request/response tunnelUDP path
// only returned a datagram when the client sent one — Steam SDR / game relays
// push unsolicited packets and died. This mirrors DialUDP streaming.
type socksUDPFlow struct {
	j      *Joiner
	key    string
	host   string
	port   int
	header []byte // SOCKS RSV+FRAG+ATYP+ADDR+PORT echoed on replies

	relay *net.UDPConn

	mu        sync.Mutex
	replyTo   *net.UDPAddr
	udp       *udpStream
	lastUsed  time.Time
	closed    chan struct{}
	closeOnce sync.Once
	pumpOnce  sync.Once
}

func (j *Joiner) ensureSharedUDPRelay() (*net.UDPConn, int, error) {
	j.udpRelayMu.Lock()
	defer j.udpRelayMu.Unlock()
	if j.udpRelayConn != nil {
		return j.udpRelayConn, j.udpRelayPort, nil
	}
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, 0, err
	}
	port := udpConn.LocalAddr().(*net.UDPAddr).Port
	j.udpRelayConn = udpConn
	j.udpRelayPort = port
	j.logFn("wbt: shared SOCKS UDP relay on 127.0.0.1:%d (bidirectional)", port)
	j.wg.Add(1)
	go j.sharedUDPRelayLoop(udpConn)
	return udpConn, port, nil
}

func (j *Joiner) closeSharedUDPRelay() {
	j.clearSocksUDPFlows()
	j.udpRelayMu.Lock()
	if j.udpRelayConn != nil {
		_ = j.udpRelayConn.Close()
		j.udpRelayConn = nil
		j.udpRelayPort = 0
	}
	j.udpRelayMu.Unlock()
}

func (j *Joiner) clearSocksUDPFlows() {
	j.socksUDPFlowsMu.Lock()
	flows := j.socksUDPFlows
	j.socksUDPFlows = make(map[string]*socksUDPFlow)
	j.socksUDPFlowsMu.Unlock()
	for _, f := range flows {
		f.Close()
	}
}

func (j *Joiner) sharedUDPRelayLoop(udpConn *net.UDPConn) {
	defer j.wg.Done()
	// Steam SDR datagrams are ~1.3KB; leave headroom for SOCKS header + growth.
	buf := make([]byte, 8192)
	var logged atomic.Uint64
	for {
		n, clientAddr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 10 {
			continue
		}
		if buf[2] != 0 { // FRAG != 0 unsupported
			continue
		}
		dstHost, headerLen, err := common.ParseAddress(buf, n)
		if err != nil {
			continue
		}
		hostOnly, portStr, _ := net.SplitHostPort(dstHost)
		port, _ := strconv.Atoi(portStr)
		payload := buf[headerLen:n]
		header := append([]byte(nil), buf[:headerLen]...)

		if common.IsTunnelSinkHost(dstHost) || common.IsNoiseDatagramHost(dstHost) {
			continue
		}
		if common.IsNonRoutableHost(dstHost) {
			j.relayUDPLocal(udpConn, clientAddr, header, dstHost, payload)
			continue
		}

		flow, err := j.getSocksUDPFlow(udpConn, hostOnly, port, header)
		if err != nil {
			continue
		}
		flow.setReplyTo(clientAddr)
		if err := flow.write(payload); err != nil {
			flow.Close()
			j.dropSocksUDPFlow(flow.key)
			continue
		}
		if n := logged.Add(1); n <= 8 || n%64 == 0 {
			j.logFn("wbt: SOCKS UDP → %s (%dB) flows=#%d", common.MaskAddr(dstHost), len(payload), n)
		}
	}
}

func (j *Joiner) getSocksUDPFlow(relay *net.UDPConn, host string, port int, header []byte) (*socksUDPFlow, error) {
	key := udpPoolKey(host, port)
	j.socksUDPFlowsMu.Lock()
	if j.socksUDPFlows == nil {
		j.socksUDPFlows = make(map[string]*socksUDPFlow)
	}
	if f := j.socksUDPFlows[key]; f != nil {
		j.socksUDPFlowsMu.Unlock()
		f.mu.Lock()
		if len(header) > 0 {
			f.header = header
		}
		f.lastUsed = time.Now()
		f.mu.Unlock()
		return f, nil
	}
	if len(j.socksUDPFlows) >= socksUDPFlowMax {
		j.socksUDPFlowsMu.Unlock()
		return nil, errString("udp flow table full")
	}
	f := &socksUDPFlow{
		j:        j,
		key:      key,
		host:     host,
		port:     port,
		header:   header,
		relay:    relay,
		closed:   make(chan struct{}),
		lastUsed: time.Now(),
	}
	j.socksUDPFlows[key] = f
	j.socksUDPFlowsMu.Unlock()
	return f, nil
}

func (j *Joiner) dropSocksUDPFlow(key string) {
	j.socksUDPFlowsMu.Lock()
	f := j.socksUDPFlows[key]
	delete(j.socksUDPFlows, key)
	j.socksUDPFlowsMu.Unlock()
	if f != nil {
		f.Close()
	}
}

func (j *Joiner) evictIdleSocksUDPFlows() {
	now := time.Now()
	j.socksUDPFlowsMu.Lock()
	var stale []*socksUDPFlow
	for k, f := range j.socksUDPFlows {
		f.mu.Lock()
		idle := now.Sub(f.lastUsed) > socksUDPFlowIdleTTL
		f.mu.Unlock()
		if idle {
			delete(j.socksUDPFlows, k)
			stale = append(stale, f)
		}
	}
	j.socksUDPFlowsMu.Unlock()
	for _, f := range stale {
		f.Close()
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func (f *socksUDPFlow) setReplyTo(addr *net.UDPAddr) {
	f.mu.Lock()
	f.replyTo = cloneUDPAddr(addr)
	f.lastUsed = time.Now()
	f.mu.Unlock()
}

func cloneUDPAddr(a *net.UDPAddr) *net.UDPAddr {
	if a == nil {
		return nil
	}
	ip := make(net.IP, len(a.IP))
	copy(ip, a.IP)
	return &net.UDPAddr{IP: ip, Port: a.Port, Zone: a.Zone}
}

func (f *socksUDPFlow) write(payload []byte) error {
	select {
	case <-f.closed:
		return net.ErrClosed
	default:
	}
	sessionstats.AddTx(uint64(len(payload)))

	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUsed = time.Now()
	if f.udp == nil {
		j := f.j
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
		f.udp = newUDPStream(stream)
		if err := writeUDPRequest(stream, f.host, f.port, payload); err != nil {
			f.udp.Close()
			f.udp = nil
			return err
		}
		f.pumpOnce.Do(func() { go f.pumpInbound() })
		return nil
	}
	return writeUDPDatagram(f.udp.stream, payload)
}

func (f *socksUDPFlow) pumpInbound() {
	for {
		f.mu.Lock()
		udp := f.udp
		f.mu.Unlock()
		if udp == nil {
			return
		}
		select {
		case <-f.closed:
			return
		case <-udp.done:
			f.Close()
			f.j.dropSocksUDPFlow(f.key)
			return
		case msg := <-udp.inbound:
			if msg == nil {
				f.Close()
				f.j.dropSocksUDPFlow(f.key)
				return
			}
			f.deliver(msg)
		}
	}
}

func (f *socksUDPFlow) deliver(payload []byte) {
	f.mu.Lock()
	relay := f.relay
	replyTo := f.replyTo
	header := f.header
	f.lastUsed = time.Now()
	f.mu.Unlock()
	if relay == nil || replyTo == nil || len(header) == 0 {
		return
	}
	out := make([]byte, 0, len(header)+len(payload))
	out = append(out, header...)
	out = append(out, payload...)
	_, _ = relay.WriteToUDP(out, replyTo)
}

func (f *socksUDPFlow) Close() {
	f.closeOnce.Do(func() {
		close(f.closed)
		f.mu.Lock()
		if f.udp != nil {
			f.udp.Close()
			f.udp = nil
		}
		f.mu.Unlock()
	})
}

func (j *Joiner) handleUDPAssociate(tcpConn net.Conn) {
	_, port, err := j.ensureSharedUDPRelay()
	if err != nil {
		_, _ = tcpConn.Write(common.GenFail)
		_ = tcpConn.Close()
		return
	}
	reply := []byte{common.Ver, 0x00, 0x00, common.AtypIPv4, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(reply[8:10], uint16(port))
	if _, err := tcpConn.Write(reply); err != nil {
		_ = tcpConn.Close()
		return
	}

	j.udpAssocMu.Lock()
	j.udpAssocCount++
	n := j.udpAssocCount
	j.udpAssocMu.Unlock()
	if n <= 3 {
		j.logFn("wbt: SOCKS UDP ASSOCIATE ok relay=127.0.0.1:%d (assoc=%d)", port, n)
	}

	go func() {
		buf := make([]byte, 1)
		_, _ = tcpConn.Read(buf)
		_ = tcpConn.Close()
		j.udpAssocMu.Lock()
		j.udpAssocCount--
		j.udpAssocMu.Unlock()
	}()
}

func (j *Joiner) relayUDPLocal(udpConn *net.UDPConn, clientAddr *net.UDPAddr, header []byte, dst string, payload []byte) {
	conn, err := net.DialTimeout("udp", dst, 3*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := conn.Write(payload); err != nil {
		return
	}
	// Local path: short request/response is fine (LAN/DNS-like).
	resp := make([]byte, 8192)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(resp)
	if err != nil || n == 0 {
		return
	}
	out := make([]byte, 0, len(header)+n)
	out = append(out, header...)
	out = append(out, resp[:n]...)
	_, _ = udpConn.WriteToUDP(out, clientAddr)
}
