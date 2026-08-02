package wbtunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
	"github.com/ildarmaga/whitelist-bypass/relay/tunnel"
	"github.com/xtaci/smux"
)

// Joiner exposes local SOCKS5 and tunnels TCP via KCP+smux to the creator.
type Joiner struct {
	logFn func(string, ...any)

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	link     *Link
	smuxSess *smux.Session
	ln       net.Listener

	socksUser string
	socksPass string

	udpPool *udpPool

	udpConns atomic.Int32 // active udpTunnelConn instances

	udpRelayMu    sync.Mutex
	udpRelayConn  *net.UDPConn
	udpRelayPort  int
	udpAssocMu    sync.Mutex
	udpAssocCount int

	socksUDPFlowsMu sync.Mutex
	socksUDPFlows   map[string]*socksUDPFlow

	closed atomic.Bool
	wg     sync.WaitGroup

	lastEpochRestart time.Time
	lastSwapAt       time.Time
	dialHoldUntil    time.Time // reject SOCKS dials while smux is restarting
	socksDialN       atomic.Uint64

	// Active local SOCKS client conns — closed on Joiner.Close so relayBoth
	// exits instead of blocking shutdown for tens of seconds (v2rayN keepalives).
	socksConnsMu sync.Mutex
	socksConns   map[net.Conn]struct{}
}

func NewJoiner(
	parent context.Context,
	tun tunnel.DataTunnel,
	socksUser, socksPass string,
	logFn func(string, ...any),
	onPeerRestart func(),
) (*Joiner, error) {
	if logFn == nil {
		logFn = func(string, ...any) {}
	}
	t0 := time.Now()
	ctx, cancel := context.WithCancel(parent)
	j := &Joiner{
		logFn:         logFn,
		ctx:           ctx,
		cancel:        cancel,
		socksUser:     socksUser,
		socksPass:     socksPass,
		udpPool:       newUDPPool(),
		socksConns:    make(map[net.Conn]struct{}),
		socksUDPFlows: make(map[string]*socksUDPFlow),
	}
	if err := j.installLink(tun, onPeerRestart); err != nil {
		cancel()
		return nil, err
	}
	j.wg.Add(1)
	go j.udpPoolEvictLoop()
	j.logFn("wbt: joiner ready (KCP+smux in %v)", time.Since(t0).Round(time.Millisecond))
	return j, nil
}

func (j *Joiner) installLink(tun tunnel.DataTunnel, _ func()) error {
	link := NewLink(tun, j.logFn)
	if err := link.Attach(j.onPeerEpochRestart); err != nil {
		return err
	}
	sess, err := smux.Client(link.Session(), smuxConfig())
	if err != nil {
		link.Close()
		return err
	}
	j.mu.Lock()
	if j.link != nil {
		j.link.Close()
	}
	if j.smuxSess != nil {
		_ = j.smuxSess.Close()
	}
	j.link = link
	j.smuxSess = sess
	j.mu.Unlock()
	j.udpPool.clear()
	j.clearSocksUDPFlows()
	return nil
}

func (j *Joiner) udpPoolEvictLoop() {
	defer j.wg.Done()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-j.ctx.Done():
			return
		case <-t.C:
			j.udpPool.evictIdle()
			j.evictIdleSocksUDPFlows()
		}
	}
}

func (j *Joiner) onPeerEpochRestart() {
	if err := j.RestartLink(false); err != nil {
		j.logFn("wbt: peer epoch restart: %v", err)
		return
	}
	j.logFn("wbt: KCP+smux restarted (peer epoch)")
}

// RestartLink resets KCP+smux on the current VP8 carrier (ICE rebind / client recovery).
func (j *Joiner) RestartLink(force bool) error {
	if j.closed.Load() {
		return netErrClosed()
	}
	j.mu.Lock()
	if !force && time.Since(j.lastEpochRestart) < 8*time.Second {
		j.mu.Unlock()
		j.logFn("wbt: RestartLink skipped (debounce <8s, force=%v)", force)
		return nil
	}
	j.lastEpochRestart = time.Now()
	j.dialHoldUntil = time.Now().Add(1500 * time.Millisecond)
	link := j.link
	j.mu.Unlock()
	if link == nil {
		return fmt.Errorf("wbt: no link")
	}
	t0 := time.Now()
	j.logFn("wbt: RestartLink begin force=%v", force)
	j.closeSOCKSConns()
	if err := link.Restart(j.onPeerEpochRestart); err != nil {
		j.logFn("wbt: RestartLink KCP failed after %v: %v", time.Since(t0).Round(time.Millisecond), err)
		return err
	}
	sess, err := smux.Client(link.Session(), smuxConfig())
	if err != nil {
		j.logFn("wbt: RestartLink smux failed after %v: %v", time.Since(t0).Round(time.Millisecond), err)
		return err
	}
	j.mu.Lock()
	if j.smuxSess != nil {
		_ = j.smuxSess.Close()
	}
	j.smuxSess = sess
	j.mu.Unlock()
	j.udpPool.clear()
	j.clearSocksUDPFlows()
	j.logFn("wbt: KCP+smux restarted in %v", time.Since(t0).Round(time.Millisecond))
	return nil
}

// SwapTunnel rebinds the VP8 carrier after WebRTC recovery and restarts KCP+smux.
// Keeping smux across ICE renegotiation left stale streams — new TCP got ERR_CONNECTION_CLOSED.
func (j *Joiner) SwapTunnel(tun tunnel.DataTunnel, onPeerRestart func()) error {
	if j.closed.Load() {
		return netErrClosed()
	}
	t0 := time.Now()
	j.mu.Lock()
	link := j.link
	// ICE often fires onConnected + sub-offer within ms; second SwapTunnel killed
	// the smux session the first one just built → OpenStream closed pipe flood.
	if link != nil && !j.lastSwapAt.IsZero() && time.Since(j.lastSwapAt) < 2*time.Second {
		j.mu.Unlock()
		j.logFn("wbt: SwapTunnel skipped (debounce <2s)")
		return nil
	}
	j.lastSwapAt = time.Now()
	// Hold new SOCKS dials across RestartLink so v2rayN doesn't OpenStream on the
	// dying smux (closed pipe flood → false full reconnect).
	j.dialHoldUntil = time.Now().Add(1500 * time.Millisecond)
	j.mu.Unlock()
	if link == nil {
		j.logFn("wbt: SwapTunnel — no link, installLink…")
		return j.installLink(tun, onPeerRestart)
	}
	j.logFn("wbt: SwapTunnel begin (Rebind+RestartLink)…")
	j.closeSOCKSConns() // drop in-flight dials on the old smux
	if err := link.Rebind(tun, j.onPeerEpochRestart); err != nil {
		j.logFn("wbt: SwapTunnel Rebind failed after %v: %v", time.Since(t0).Round(time.Millisecond), err)
		return err
	}
	if err := j.RestartLink(true); err != nil {
		j.logFn("wbt: SwapTunnel RestartLink failed after %v: %v", time.Since(t0).Round(time.Millisecond), err)
		return err
	}
	j.logFn("wbt: tunnel carrier rebound (KCP+smux restarted) total=%v", time.Since(t0).Round(time.Millisecond))
	return nil
}

func (j *Joiner) ListenSOCKS(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return j.ServeSOCKS(ln)
}

// ServeSOCKS runs the SOCKS5 accept loop on an already-bound listener.
// Callers that need to verify the bind succeeded before taking further
// action (e.g. bringing up a full-device TUN) can net.Listen themselves
// and hand the listener here.
func (j *Joiner) ServeSOCKS(ln net.Listener) error {
	j.mu.Lock()
	if j.closed.Load() {
		j.mu.Unlock()
		ln.Close()
		return netErrClosed()
	}
	j.ln = ln
	j.mu.Unlock()
	j.logFn("wbt: SOCKS5 on %s", ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			if j.closed.Load() || isClosedListenerErr(err) {
				return nil
			}
			j.logFn("wbt: accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		j.wg.Add(1)
		go func() {
			defer j.wg.Done()
			j.handleSOCKS(conn)
		}()
	}
}

func (j *Joiner) trackSOCKSConn(c net.Conn) {
	j.socksConnsMu.Lock()
	j.socksConns[c] = struct{}{}
	j.socksConnsMu.Unlock()
}

func (j *Joiner) untrackSOCKSConn(c net.Conn) {
	j.socksConnsMu.Lock()
	delete(j.socksConns, c)
	j.socksConnsMu.Unlock()
}

func (j *Joiner) closeSOCKSConns() {
	j.socksConnsMu.Lock()
	conns := make([]net.Conn, 0, len(j.socksConns))
	for c := range j.socksConns {
		conns = append(conns, c)
	}
	j.socksConns = make(map[net.Conn]struct{})
	j.socksConnsMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (j *Joiner) Close() {
	if !j.closed.CompareAndSwap(false, true) {
		return
	}
	j.cancel()
	j.mu.Lock()
	if j.ln != nil {
		_ = j.ln.Close()
		j.ln = nil
	}
	if j.smuxSess != nil {
		_ = j.smuxSess.Close()
		j.smuxSess = nil
	}
	if j.link != nil {
		j.link.Close()
		j.link = nil
	}
	j.closeSharedUDPRelay()
	j.udpPool.clear()
	j.mu.Unlock()
	j.closeSOCKSConns()
	done := make(chan struct{})
	go func() {
		j.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		j.logFn("wbt: joiner Close — SOCKS relays still draining (timeout 2s)")
	}
}

func (j *Joiner) handleSOCKS(conn net.Conn) {
	j.trackSOCKSConn(conn)
	defer func() {
		j.untrackSOCKSConn(conn)
		_ = conn.Close()
	}()
	buf := make([]byte, common.HandshakeBuf)
	n, err := conn.Read(buf)
	if err != nil || n < 2 || buf[0] != common.Ver {
		return
	}
	if !common.NegotiateAuth(conn, buf, n, j.socksUser, j.socksPass) {
		return
	}
	n, err = conn.Read(buf)
	if err != nil || n < 7 || buf[0] != common.Ver {
		return
	}
	switch buf[1] {
	case common.CmdUDP:
		j.handleUDPAssociate(conn)
		return
	case common.CmdTCP:
		// continue below
	default:
		_, _ = conn.Write(common.CmdErr)
		return
	}
	host, _, err := common.ParseAddress(buf, n)
	if err != nil {
		_, _ = conn.Write(common.AddrErr)
		return
	}
	hostOnly, _, _ := net.SplitHostPort(host)
	if ip := net.ParseIP(hostOnly); ip != nil && ip.IsUnspecified() {
		_, _ = conn.Write(common.ConnFail)
		return
	}
	if common.IsNonRoutableHost(host) {
		if ip := net.ParseIP(hostOnly); ip != nil && (ip[0] == 198 || common.IsTunnelSinkHost(host)) {
			j.logFn("wbt: reject sink %s", common.MaskAddr(host))
			_, _ = conn.Write(common.ConnFail)
			return
		}
		j.logFn("wbt: local dial %s", common.MaskAddr(host))
		target, dialErr := net.DialTimeout("tcp", host, 10*time.Second)
		if dialErr != nil {
			_, _ = conn.Write(common.ConnFail)
			return
		}
		common.EnableTCPNoDelay(target)
		_, _ = conn.Write(common.OK)
		j.relayBoth(conn, target)
		return
	}

	j.mu.Lock()
	sess := j.smuxSess
	hold := !j.dialHoldUntil.IsZero() && time.Now().Before(j.dialHoldUntil)
	j.mu.Unlock()
	if hold {
		// Quiet reject during SwapTunnel/RestartLink — not an error to escalate on.
		_, _ = conn.Write(common.ConnFail)
		return
	}
	if sess == nil {
		_, _ = conn.Write(common.ConnFail)
		return
	}

	hostOnly, portStr, _ := net.SplitHostPort(host)

	nDial := j.socksDialN.Add(1)
	tDial := time.Now()
	stream, err := sess.OpenStream()
	if err != nil {
		j.logFn("wbt: OpenStream #%d %s: %v (after %v)", nDial, common.MaskAddr(host), err, time.Since(tDial).Round(time.Millisecond))
		_, _ = conn.Write(common.ConnFail)
		return
	}
	defer stream.Close()

	port, _ := strconv.Atoi(portStr)
	if err := j.sendConnectRequest(stream, hostOnly, port); err != nil {
		j.logFn("wbt: tunnel #%d %s failed after %v: %s", nDial, common.MaskAddr(host), time.Since(tDial).Round(time.Millisecond), common.MaskError(err))
		_, _ = conn.Write(common.ConnFail)
		return
	}
	dialMs := time.Since(tDial)
	// First 12 dials + any slow (>800ms) — full tunnel visibility without speedtest spam.
	if nDial <= 12 || dialMs > 800*time.Millisecond {
		j.logFn("wbt: SOCKS dial #%d %s ok in %v", nDial, common.MaskAddr(host), dialMs.Round(time.Millisecond))
	}
	_, _ = conn.Write(common.OK)
	j.relayBoth(conn, stream)
}

func (j *Joiner) sendConnectRequest(stream *smux.Stream, targetAddr string, targetPort int) error {
	req, err := json.Marshal(ConnectRequest{
		Cmd:  connectCommand,
		Addr: targetAddr,
		Port: targetPort,
	})
	if err != nil {
		return err
	}
	_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := stream.Write(req); err != nil {
		return fmt.Errorf("write connect: %w", err)
	}
	_ = stream.SetWriteDeadline(time.Time{})

	ack := make([]byte, 1)
	// Creator dial timeout is 20s; wait a little longer, not 90s (zombie slots).
	_ = stream.SetReadDeadline(time.Now().Add(25 * time.Second))
	if _, err := io.ReadFull(stream, ack); err != nil {
		return fmt.Errorf("remote not ready: %w", err)
	}
	_ = stream.SetReadDeadline(time.Time{})
	switch ack[0] {
	case 0x00:
		return nil
	case 0x01:
		return fmt.Errorf("remote dial failed")
	default:
		// Other non-zero with no read error → smux desync (stale carrier bytes).
		return fmt.Errorf("remote not ready: smux desync ack=0x%02x", ack[0])
	}
}

func (j *Joiner) relayBoth(a, b net.Conn) {
	done := make(chan struct{})
	go func() {
		_, _ = sessionstats.Copy(b, a, false)
		close(done)
	}()
	_, _ = sessionstats.Copy(a, b, true)
	<-done
}

func (j *Joiner) RTTMs() int {
	j.mu.Lock()
	link := j.link
	j.mu.Unlock()
	if link == nil {
		return 0
	}
	return link.RTTMs()
}

func isClosedListenerErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}
