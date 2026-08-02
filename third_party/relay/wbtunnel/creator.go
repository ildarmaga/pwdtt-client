package wbtunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/tunnel"
	"github.com/xtaci/smux"
	"golang.org/x/time/rate"
)

const connectCommand = "connect"

// ConnectRequest is sent by the joiner on each new smux stream (TCP).
type ConnectRequest = StreamRequest

// Creator terminates KCP+smux on the server and dials targets via upstream SOCKS.
type Creator struct {
	logFn             func(string, ...any)
	onTraffic         func(up, down int64)
	onJoinerActivity  func()
	onCarrierActivity func()

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	link     *Link
	smuxSess *smux.Session
	upstream *common.Socks5Upstream

	closed atomic.Bool
	wg     sync.WaitGroup

	// Active dialed TCP/UDP sockets. Close() shuts them so dispatchTCP/UDP
	// workers unblock instead of wedging Creator.Close on wg.Wait forever
	// (which blocked SFU rejoin → "owner offline" / 403 for joiners).
	activeConns sync.Map // net.Conn → struct{}

	lastEpochRestart time.Time
	lastSwapAt       time.Time
	udpDialN         atomic.Uint64

	// Live speed limits (MB/s). Updated via SetSpeedLimitsMB without reconnect.
	limMu   sync.RWMutex
	downLim *rate.Limiter // creator→joiner (download to client)
	upLim   *rate.Limiter // joiner→creator (upload from client)
}

func NewCreator(
	parent context.Context,
	tun tunnel.DataTunnel,
	upstreamAddr, upstreamUser, upstreamPass string,
	logFn func(string, ...any),
	onPeerRestart func(),
	onTraffic func(up, down int64),
) (*Creator, error) {
	if logFn == nil {
		logFn = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(parent)
	c := &Creator{
		logFn:     logFn,
		onTraffic: onTraffic,
		ctx:       ctx,
		cancel:    cancel,
	}
	if upstreamAddr != "" {
		c.upstream = common.NewSocks5Upstream(upstreamAddr, upstreamUser, upstreamPass)
	}
	if err := c.installLink(tun, onPeerRestart); err != nil {
		cancel()
		return nil, err
	}
	c.wg.Add(1)
	go c.acceptLoop()
	c.logFn("wbt: creator started upstream=%s", upstreamAddr)
	return c, nil
}

// SetSpeedLimitsMB sets download/upload caps in megabytes/sec (0 = unlimited).
// Applies to new and in-flight TCP copies (WaitN on each Write).
func (c *Creator) SetSpeedLimitsMB(downMBps, upMBps float64) {
	if c == nil {
		return
	}
	c.limMu.Lock()
	c.downLim = mbpsToLimiter(downMBps)
	c.upLim = mbpsToLimiter(upMBps)
	c.limMu.Unlock()
	if downMBps > 0 || upMBps > 0 {
		c.logFn("wbt: speed limit ↓%.3f ↑%.3f MB/s", downMBps, upMBps)
	}
}

func (c *Creator) getDownLim() *rate.Limiter {
	c.limMu.RLock()
	defer c.limMu.RUnlock()
	return c.downLim
}

func (c *Creator) getUpLim() *rate.Limiter {
	c.limMu.RLock()
	defer c.limMu.RUnlock()
	return c.upLim
}

// SetOnJoinerActivity fires when the joiner opens a smux stream (CONNECT/UDP).
func (c *Creator) SetOnJoinerActivity(fn func()) {
	c.mu.Lock()
	c.onJoinerActivity = fn
	c.mu.Unlock()
}

// SetOnCarrierActivity fires on each inbound VP8 carrier frame (KCP keepalive).
func (c *Creator) SetOnCarrierActivity(fn func()) {
	c.mu.Lock()
	c.onCarrierActivity = fn
	link := c.link
	c.mu.Unlock()
	if link != nil {
		link.SetOnCarrierActivity(fn)
	}
}

func (c *Creator) installLink(tun tunnel.DataTunnel, _ func()) error {
	link := NewLink(tun, c.logFn)
	c.mu.Lock()
	carrierFn := c.onCarrierActivity
	c.mu.Unlock()
	if carrierFn != nil {
		link.SetOnCarrierActivity(carrierFn)
	}
	if err := link.Attach(c.onPeerEpochRestart); err != nil {
		return err
	}
	sess, err := smux.Server(link.Session(), smuxConfig())
	if err != nil {
		link.Close()
		return err
	}
	c.mu.Lock()
	if c.link != nil {
		c.link.Close()
	}
	if c.smuxSess != nil {
		_ = c.smuxSess.Close()
	}
	c.link = link
	c.smuxSess = sess
	c.mu.Unlock()
	return nil
}

func (c *Creator) onPeerEpochRestart() {
	c.mu.Lock()
	if time.Since(c.lastEpochRestart) < 8*time.Second {
		c.mu.Unlock()
		return
	}
	c.lastEpochRestart = time.Now()
	link := c.link
	c.mu.Unlock()
	c.logFn("wbt: peer epoch restart, resetting KCP+smux")
	if link == nil {
		return
	}
	if err := link.Restart(c.onPeerEpochRestart); err != nil {
		c.logFn("wbt: link restart: %v", err)
		return
	}
	if err := c.rebindSmuxServer(); err != nil {
		c.logFn("wbt: smux server: %v", err)
	}
}

func (c *Creator) rebindSmuxServer() error {
	c.mu.Lock()
	link := c.link
	if c.smuxSess != nil {
		_ = c.smuxSess.Close()
		c.smuxSess = nil
	}
	c.mu.Unlock()
	if link == nil {
		return fmt.Errorf("wbt: no link")
	}
	sess, err := smux.Server(link.Session(), smuxConfig())
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.smuxSess = sess
	c.mu.Unlock()
	c.logFn("wbt: smux server rebound")
	return nil
}

// SwapTunnel rebinds the VP8 carrier after WebRTC recovery and restarts KCP+smux.
func (c *Creator) SwapTunnel(tun tunnel.DataTunnel, onPeerRestart func()) error {
	if c.closed.Load() {
		return netErrClosed()
	}
	c.mu.Lock()
	link := c.link
	if link != nil && !c.lastSwapAt.IsZero() && time.Since(c.lastSwapAt) < 2*time.Second {
		c.mu.Unlock()
		c.logFn("wbt: SwapTunnel skipped (debounce <2s)")
		return nil
	}
	c.lastSwapAt = time.Now()
	c.mu.Unlock()
	if link == nil {
		return c.installLink(tun, onPeerRestart)
	}
	if err := link.Rebind(tun, c.onPeerEpochRestart); err != nil {
		return err
	}
	if err := link.Restart(c.onPeerEpochRestart); err != nil {
		return err
	}
	if err := c.rebindSmuxServer(); err != nil {
		return err
	}
	c.logFn("wbt: tunnel carrier rebound (KCP+smux restarted)")
	return nil
}

// RestartLink resets KCP+smux on the current VP8 carrier (peer epoch / ICE recovery).
func (c *Creator) RestartLink() error {
	if c.closed.Load() {
		return netErrClosed()
	}
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return fmt.Errorf("wbt: no link")
	}
	if err := link.Restart(c.onPeerEpochRestart); err != nil {
		return err
	}
	return c.rebindSmuxServer()
}

func (c *Creator) Close() {
	if !c.closed.CompareAndSwap(false, true) {
		return
	}
	c.cancel()
	c.activeConns.Range(func(k, _ any) bool {
		if conn, ok := k.(net.Conn); ok {
			_ = conn.Close()
		}
		return true
	})
	c.mu.Lock()
	if c.smuxSess != nil {
		_ = c.smuxSess.Close()
		c.smuxSess = nil
	}
	if c.link != nil {
		c.link.Close()
		c.link = nil
	}
	c.mu.Unlock()
	// Never block rejoin on a stuck io.Copy: field hang left the room without
	// an SFU owner while KCP still ticked on the dead link.
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		c.logFn("wbt: creator Close: stream workers still draining after 3s — continuing")
	}
}

func (c *Creator) trackConn(conn net.Conn) {
	if conn == nil {
		return
	}
	c.activeConns.Store(conn, struct{}{})
}

func (c *Creator) untrackConn(conn net.Conn) {
	if conn == nil {
		return
	}
	c.activeConns.Delete(conn)
}

func (c *Creator) acceptLoop() {
	defer c.wg.Done()
	for {
		if c.ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		sess := c.smuxSess
		c.mu.Unlock()
		if sess == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		stream, err := sess.AcceptStream()
		if err != nil {
			if c.ctx.Err() != nil || c.closed.Load() {
				return
			}
			// smux session dead; wait for epoch restart or tunnel swap to replace it.
			time.Sleep(500 * time.Millisecond)
			continue
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.handleStream(stream)
		}()
	}
}

func (c *Creator) handleStream(stream *smux.Stream) {
	defer func() { _ = stream.Close() }()

	_ = stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	dec := json.NewDecoder(stream)
	var req StreamRequest
	if err := dec.Decode(&req); err != nil {
		return
	}
	switch req.Cmd {
	case connectCommand, udpCommand:
	default:
		return
	}
	c.mu.Lock()
	fn := c.onJoinerActivity
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
	_ = stream.SetReadDeadline(time.Time{})
	leftover := dec.Buffered()
	switch req.Cmd {
	case connectCommand:
		c.dispatchTCP(stream, req, leftover)
	case udpCommand:
		c.dispatchUDP(stream, req, leftover)
	}
}

func parseConnectRequest(buf []byte) (ConnectRequest, bool) {
	return parseStreamRequest(buf)
}

func (c *Creator) dispatchTCP(stream *smux.Stream, req StreamRequest, leftover io.Reader) {
	addr := net.JoinHostPort(req.Addr, strconv.Itoa(req.Port))
	c.logFn("wbt: connect sid=%d -> %s", stream.ID(), common.MaskAddr(addr))

	conn, err := c.dial(addr)
	if err != nil {
		c.logFn("wbt: dial %s failed: %s", common.MaskAddr(addr), common.MaskError(err))
		// Failure ack (0x01) so the joiner fails fast instead of waiting on read timeout.
		_, _ = stream.Write([]byte{0x01})
		return
	}
	c.trackConn(conn)
	defer func() {
		c.untrackConn(conn)
		_ = conn.Close()
	}()
	common.EnableTCPNoDelay(conn)

	if _, err := stream.Write([]byte{0x00}); err != nil {
		return
	}

	var up, down int64
	flush := func() {
		u := atomic.SwapInt64(&up, 0)
		d := atomic.SwapInt64(&down, 0)
		if c.onTraffic != nil && (u > 0 || d > 0) {
			c.onTraffic(u, d)
		}
	}
	done := make(chan struct{})
	go func() {
		// Upstream → joiner = download to client
		dw := &limitedWriter{ctx: c.ctx, w: &countingWriter{w: stream, n: &down}, getLim: c.getDownLim}
		_, _ = io.Copy(dw, conn)
		close(done)
	}()
	// Abort copies when Creator is closed so Close()/rejoin cannot wedge.
	go func() {
		select {
		case <-c.ctx.Done():
			_ = conn.Close()
			_ = stream.Close()
		case <-done:
		}
	}()
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	go func() {
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				flush()
			}
		}
	}()
	// Joiner → upstream = upload from client
	uw := &limitedWriter{ctx: c.ctx, w: &countingWriter{w: conn, n: &up}, getLim: c.getUpLim}
	_, _ = io.Copy(uw, streamReader(stream, leftover))
	<-done
	flush()
}

func streamReader(stream *smux.Stream, leftover io.Reader) io.Reader {
	if leftover == nil {
		return stream
	}
	return io.MultiReader(leftover, stream)
}

func (c *Creator) dispatchUDP(stream *smux.Stream, req StreamRequest, leftover io.Reader) {
	addr := net.JoinHostPort(req.Addr, strconv.Itoa(req.Port))
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return
	}

	var (
		socksUDP *common.Socks5UDPSession
		conn     *net.UDPConn
	)
	if c.upstream != nil {
		s, err := c.upstream.UDPAssociate(15 * time.Second)
		if err != nil {
			c.logFn("wbt: udp socks %s: %v", common.MaskAddr(addr), err)
			return
		}
		defer s.Close()
		socksUDP = s
	} else {
		udpConn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			c.logFn("wbt: udp dial %s: %v", common.MaskAddr(addr), err)
			return
		}
		defer udpConn.Close()
		conn = udpConn
		c.trackConn(udpConn)
		defer c.untrackConn(udpConn)
	}

	stop := make(chan struct{})
	var stopOnce sync.Once
	stopFn := func() { stopOnce.Do(func() { close(stop) }) }
	defer stopFn()
	go func() {
		select {
		case <-c.ctx.Done():
			stopFn()
			_ = stream.Close()
			if socksUDP != nil {
				socksUDP.Close()
			}
			if conn != nil {
				_ = conn.Close()
			}
		case <-stop:
		}
	}()

	// smux <- UDP (unsolicited server packets — required for game relays / Steam SDR)
	go func() {
		readFn := func(b []byte) (int, error) {
			if socksUDP != nil {
				_ = socksUDP.SetReadDeadline(time.Now().Add(udpRelayIdle))
				return socksUDP.Read(b)
			}
			_ = conn.SetReadDeadline(time.Now().Add(udpRelayIdle))
			return conn.Read(b)
		}
		relayUDPUpstream(stream, stop, readFn)
	}()

	// smux -> UDP (first datagram may be coalesced with JSON header)
	r := streamReader(stream, leftover)
	for {
		select {
		case <-stop:
			return
		case <-c.ctx.Done():
			return
		default:
		}
		payload, err := readUDPPayload(r)
		if err != nil {
			return
		}
		if len(payload) == 0 {
			continue
		}
		if n := c.udpDialN.Add(1); n <= 8 || n%64 == 0 {
			c.logFn("wbt: udp sid=%d -> %s (%dB) #%d", stream.ID(), common.MaskAddr(addr), len(payload), n)
		}
		if socksUDP != nil {
			if err := socksUDP.WriteTo(payload, addr); err != nil {
				return
			}
		} else if _, err := conn.Write(payload); err != nil {
			return
		}
	}
}

type countingWriter struct {
	w io.Writer
	n *int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 {
		atomic.AddInt64(cw.n, int64(n))
	}
	return n, err
}

func (c *Creator) dial(addr string) (net.Conn, error) {
	if c.upstream != nil {
		conn, err := c.upstream.DialTCP(addr, 20*time.Second)
		if err == nil {
			return conn, nil
		}
		host, port, splitErr := net.SplitHostPort(addr)
		if splitErr == nil && net.ParseIP(host) == nil {
			ips, resErr := net.LookupIP(host)
			if resErr == nil {
				for _, ip := range ips {
					if v4 := ip.To4(); v4 != nil {
						ipAddr := net.JoinHostPort(v4.String(), port)
						if conn2, err2 := c.upstream.DialTCP(ipAddr, 20*time.Second); err2 == nil {
							return conn2, nil
						}
					}
				}
			}
		}
		return nil, err
	}
	return net.DialTimeout("tcp", addr, 20*time.Second)
}
