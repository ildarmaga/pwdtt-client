package wbtunnel

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/tunnel"
	kcp "github.com/xtaci/kcp-go/v5"
)

const (
	kcpConvID     = 0xC0FFEE01
	kcpMTU        = 1200
	kcpSndWndBase = 2048
	kcpRcvWndBase = 4096
	kcpSndWndMax  = 8192
	kcpRcvWndMax  = 16384
	// Fixed large send window — no AIMD shrink. Reference whitelist-bypass
	// (kulikov0) has no KCP window controller; our previous delay-based AIMD
	// pinned wnd=64 under load and killed SOCKS throughput.
	kcpSndWndCap  = 2048
	outboundQueue = 4096
	// Separate small priority lane for KCP ACK/control packets. Kept generous so
	// a download's ACK stream never blocks even while the bulk lane is backlogged
	// by an upload burst; ACKs are tiny and drained first, so it rarely fills.
	outboundHiQueue = 2048
	inboundQueue    = 4096
	maxKCPWireFrame = 0xffff
	// Coalesce multiple KCP segments into one VP8 sample (peer parses length-prefixed chain).
	kcpVP8BatchMax  = 1350
	kcpVP8FlushWait = 3 * time.Millisecond

	// Delay-based window control (Vegas-style). The VP8/TURN carrier drops packets
	// at random and is rate-limited, so KCP's own loss-based cwnd (nc=0) collapses
	// into a sawtooth, while nc=1 with a fixed large window blindly keeps ~1.2 MB
	// in flight until RTT balloons into multi-second bufferbloat and throughput
	// hits 0 B/s (observed: WBT RTT 261ms→3306ms, ↓ stuck). Instead we run nc=1
	// (KCP does no cwnd of its own) and make the send window itself the controller:
	// grow it while RTT sits near the floor (propagation delay, path free), shrink
	// it the moment RTT inflates past the floor (queue building). In-flight bytes
	// then track the true BDP and drain the carrier buffer before it runs away.
	kcpSndWndMin     = 128
	kcpCCGrowRatio   = 1.15
	kcpCCShrinkRatio = 1.30
	kcpCCMarginMs    = 15
	kcpWndGrowStep   = 64
	kcpWndShrinkFloor = 0.25
	kcpWndShrinkCeil  = 0.80
	kcpWndStart       = 2048 // fixed large window — no AIMD shrink under load
	kcpRTTFloorAdapt  = 0.01
	kcpShrinkStreak  = 2
	kcpWndSoftShrink = 0.90
	kcpRTTFastWindow = 4

	// Fixed window mode: keep send window at kcpWndStart; only retune interval/resend.
	// AIMD previously pinned wnd=64 under load and killed SOCKS throughput.
	kcpFixedWindow = true

	kcpTuneInterval = 500 * time.Millisecond
)

var kcpFramePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, kcpVP8BatchMax+128)
		return &b
	},
}

type rawSender interface {
	SendRaw(data []byte)
}

type dualTrackSender interface {
	rawSender
	SubTunnelCount() int
}

type peerRestartSetter interface {
	SetOnPeerRestart(fn func())
}

// Link bridges a VP8 DataTunnel with a KCP session (smux rides on top).
type Link struct {
	tun   tunnel.DataTunnel
	logFn func(string, ...any)

	outbound   chan []byte // bulk data
	outboundHi chan []byte // ACK/control — sent ahead of bulk
	stopCh     chan struct{}
	kc         *kcpConn
	sess       *kcp.UDPSession

	mu                sync.Mutex
	closed            bool
	onDataFn          func([]byte)
	onCarrierActivity func()
	reorder           *frameReorder
	rttEwma           float64
}

func NewLink(tun tunnel.DataTunnel, logFn func(string, ...any)) *Link {
	if logFn == nil {
		logFn = func(string, ...any) {}
	}
	return &Link{
		tun:   tun,
		logFn: logFn,
	}
}

func (l *Link) Attach(onPeerRestart func()) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return netErrClosed()
	}
	if l.sess != nil {
		return nil
	}
	return l.startLocked(onPeerRestart)
}

// Restart tears down KCP and starts a fresh session on the same VP8 tunnel.
func (l *Link) Restart(onPeerRestart func()) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return netErrClosed()
	}
	l.stopLocked()
	l.mu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return netErrClosed()
	}
	return l.startLocked(onPeerRestart)
}

// Rebind switches the VP8 carrier without tearing down KCP/smux (WebRTC reconnect).
func (l *Link) Rebind(tun tunnel.DataTunnel, onPeerRestart func()) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return netErrClosed()
	}
	if l.sess == nil {
		l.mu.Unlock()
		return l.Attach(onPeerRestart)
	}
	l.tun = tun
	l.reorder = nil
	// Dual VP8 is fingerprint-only; KCP is camera-only without seq prefix.
	// Enabling reorder here would strip the first 4 payload bytes and corrupt KCP.
	l.mu.Unlock()

	l.onDataFn = l.handleVP8Payload
	tun.SetOnData(l.onDataFn)
	restart := onPeerRestart
	if restart == nil {
		restart = func() { _ = l.Restart(nil) }
	}
	if prs, ok := tun.(peerRestartSetter); ok {
		prs.SetOnPeerRestart(restart)
	}
	l.logFn("wbt: KCP link rebound")
	return nil
}

func (l *Link) startLocked(onPeerRestart func()) error {
	l.outbound = make(chan []byte, outboundQueue)
	l.outboundHi = make(chan []byte, outboundHiQueue)
	l.stopCh = make(chan struct{})
	l.kc = newKCPConn(l.outbound, l.outboundHi, inboundQueue)
	// Pass the channels/stop by value: stopLocked nils the fields under l.mu, so
	// the pump goroutine must never read them unsynchronized (data race).
	go l.pumpOutbound(l.outbound, l.outboundHi, l.stopCh)
	go l.tuneLoop(l.stopCh)

	sess, err := kcp.NewConn3(kcpConvID, fakeUDPAddr(), nil, 0, 0, l.kc)
	if err != nil {
		l.stopLocked()
		return err
	}
	applyKCPProfile(sess, 0, kcpWndStart)
	sess.SetMtu(kcpMTU)
	sess.SetStreamMode(true) //nolint:staticcheck // required for smux byte stream
	sess.SetACKNoDelay(true)
	sess.SetWriteDelay(false)

	l.sess = sess
	l.onDataFn = l.handleVP8Payload
	l.tun.SetOnData(l.onDataFn)
	l.reorder = nil
	// Dual VP8 is fingerprint-only; do not enable seq reorder (see Rebind).
	restart := onPeerRestart
	if restart == nil {
		restart = func() { _ = l.Restart(nil) }
	}
	if prs, ok := l.tun.(peerRestartSetter); ok {
		prs.SetOnPeerRestart(restart)
	}
	l.logFn("wbt: KCP link ready snd=%d rcv=%d batch=%d", kcpWndStart, kcpRcvWndBase, kcpVP8BatchMax)
	return nil
}

// applyKCPProfile tunes nodelay/interval/windows for link RTT (0 = default/mobile-safe).
// sndWnd is the delay-controlled send window (in-flight cap) computed by tuneLoop;
// KCP runs with nc=1 (no internal cwnd) so the send window is the sole controller.
func applyKCPProfile(sess *kcp.UDPSession, rttMs int, sndWnd int) {
	interval := 20
	resend := 2
	rcv := kcpRcvWndBase

	switch {
	case rttMs > 500:
		interval = 50
		resend = 3
		rcv = scaleKCPWnd(kcpRcvWndBase, rttMs, kcpRcvWndMax)
	case rttMs > 250:
		interval = 40
		resend = 2
		rcv = scaleKCPWnd(kcpRcvWndBase, rttMs, kcpRcvWndMax)
	case rttMs > 120:
		interval = 30
		rcv = scaleKCPWnd(kcpRcvWndBase, rttMs, kcpRcvWndMax)
	case rttMs > 0 && rttMs <= 80:
		interval = 10
	}

	if sndWnd < kcpSndWndMin {
		sndWnd = kcpSndWndMin
	}
	if sndWnd > kcpSndWndCap {
		sndWnd = kcpSndWndCap
	}

	// nc=1: KCP does no congestion control of its own — the delay-based send
	// window below is the controller. On a randomly-lossy carrier nc=0 misreads
	// loss as congestion and collapses; here loss only triggers retransmit while
	// the window follows RTT (real queueing), not packet loss.
	sess.SetNoDelay(1, interval, resend, 1)
	sess.SetWindowSize(sndWnd, rcv)
}

// nextKCPWnd applies delay-based AIMD to the send window: additive-increase while
// RTT sits near the floor (path uncongested), multiplicative-decrease the moment
// RTT inflates past it (queue building). This drains the carrier buffer before it
// runs away into multi-second bufferbloat, which the old fixed/nc-toggle scheme
// could not do. Returns the new window clamped to [min,cap].
// nextKCPWnd picks the next send window. rttEwma is the slow smoothed RTT used
// for the SHRINK decision (jitter-resistant, so lossy-TURN blips don't collapse
// a fast flow). rttFast is a fast recent-minimum RTT used for the GROW decision:
// recovery must NOT be gated on the slow ewma, because after an upload-burst
// spike (e.g. 1232ms) the ewma takes ~10-20 ticks to decay below the grow
// threshold and pins the window at the floor (64) for tens of seconds, throttling
// the download into a sawtooth long after the path actually cleared (field:
// rtt=44ms but wnd=64 for 90s). The recent-min RTT returns to the floor within a
// tick or two of real relief, so the window grows back promptly — Vegas/BBR use
// min-RTT, not smoothed RTT, precisely for this "is the path free" question.
func nextKCPWnd(wnd int, rttEwma, rttFast, rttFloor float64, highStreak int) (int, int) {
	if rttFloor <= 0 || rttEwma <= 0 {
		return wnd, 0
	}
	if rttFast <= 0 {
		rttFast = rttEwma
	}
	growThresh := rttFloor*kcpCCGrowRatio + kcpCCMarginMs
	shrinkThresh := rttFloor*kcpCCShrinkRatio + kcpCCMarginMs
	// GROW before SHRINK: after an upload burst ewma can sit above shrinkThresh
	// for many ticks while recent-min RTT has already returned near the floor
	// (field 0.3.200: floor=155 ewma=250 rttFast≈185 → growThresh=193). If shrink
	// is checked first, grow is unreachable and wnd stays pinned at 64.
	switch {
	case rttFast < growThresh:
		// Path is genuinely free right now (recent-min RTT near floor): grow, even
		// if the slow ewma is still elevated from a recent spike.
		highStreak = 0
		wnd += kcpWndGrowStep
	case rttEwma > shrinkThresh:
		highStreak++
		if highStreak >= kcpShrinkStreak {
			// Sustained bloat: proportional cut — the more RTT is inflated over the
			// floor, the harder we shrink, so a bloated carrier drains in a tick or
			// two instead of ~8.
			factor := rttFloor / rttEwma
			if factor > kcpWndShrinkCeil {
				factor = kcpWndShrinkCeil
			}
			if factor < kcpWndShrinkFloor {
				factor = kcpWndShrinkFloor
			}
			wnd = int(float64(wnd) * factor)
		} else {
			// First high tick may be lossy-TURN jitter, not real queueing: trim
			// gently so a fast download doesn't collapse on a transient blip.
			wnd = int(float64(wnd) * kcpWndSoftShrink)
		}
	default:
		// Dead-band between grow and shrink: not congested, hold and reset streak.
		highStreak = 0
	}
	if wnd < kcpSndWndMin {
		wnd = kcpSndWndMin
	}
	if wnd > kcpSndWndCap {
		wnd = kcpSndWndCap
	}
	return wnd, highStreak
}

// updateRTTFloor tracks the low-water RTT: it snaps down instantly to a new
// minimum and creeps up very slowly, so a genuine path change is eventually
// followed while transient bufferbloat can't drag the baseline up (which would
// hide congestion and let the window keep growing).
//
// Crucially the upward creep is frozen while the path is congested (RTT above the
// shrink threshold). Otherwise a sustained multi-second stall — where the gap
// (rttEwma-floor) is huge — would drag the floor up hundreds of ms per tick even
// at a 0.01 rate, raising the shrink threshold and letting the window stop
// shrinking. Field logs showed exactly this: floor climbing 91→1609→2795ms while
// RTT ran away to 11s. Only sampling the floor upward when RTT is near baseline
// keeps the congestion signal honest.
func updateRTTFloor(floor, rttEwma float64) float64 {
	if rttEwma <= 0 {
		return floor
	}
	if floor <= 0 || rttEwma < floor {
		return rttEwma
	}
	// Congested (queue building): hold the baseline so shrink stays armed.
	if rttEwma > floor*kcpCCShrinkRatio+kcpCCMarginMs {
		return floor
	}
	return floor + (rttEwma-floor)*kcpRTTFloorAdapt
}

func scaleKCPWnd(base, rttMs, max int) int {
	// Scale in-flight window with BDP estimate: more RTT → more packets in flight.
	extra := (rttMs / 100) * (base / 4)
	if extra < 0 {
		extra = 0
	}
	w := base + extra
	if w > max {
		return max
	}
	return w
}

func (l *Link) tuneLoop(stopCh <-chan struct{}) {
	// Sub-second cadence: an upload burst inflates RTT within a couple of seconds,
	// so the window must be cut fast enough to drain the carrier before the stall
	// forms. See kcpTuneInterval.
	t := time.NewTicker(kcpTuneInterval)
	defer t.Stop()
	lastLog := time.Time{}
	var rttEwma, rttFloor float64
	wnd := kcpWndStart
	highStreak := 0
	// Recent raw-SRTT ring for the fast recovery (min-RTT) signal. A short window
	// (kcpRTTFastWindow ticks) tracks current conditions: it returns to the floor
	// within a tick or two of real relief, unlike the slow rttEwma.
	rttRing := make([]int, 0, kcpRTTFastWindow)
	for {
		select {
		case <-stopCh:
			return
		case <-t.C:
			l.mu.Lock()
			sess := l.sess
			l.mu.Unlock()
			if sess == nil {
				continue
			}
			rtt := int(sess.GetSRTT())
			if rtt <= 0 {
				rtt = int(sess.GetRTO())
			}
			if rtt <= 0 {
				continue
			}
			// Smooth RTT for tuning — raw SRTT spikes (800ms+) during speedtest
			// burst would flip interval 20→50 and cause visible speed dips.
			if rttEwma <= 0 {
				rttEwma = float64(rtt)
			} else {
				rttEwma = rttEwma*0.88 + float64(rtt)*0.12
			}
			// Fast recovery signal: minimum raw SRTT over the last few ticks.
			if len(rttRing) == kcpRTTFastWindow {
				rttRing = rttRing[1:]
			}
			rttRing = append(rttRing, rtt)
			rttFast := rttRing[0]
			for _, v := range rttRing[1:] {
				if v < rttFast {
					rttFast = v
				}
			}
			rttFloor = updateRTTFloor(rttFloor, rttEwma)
			if !kcpFixedWindow {
				wnd, highStreak = nextKCPWnd(wnd, rttEwma, float64(rttFast), rttFloor, highStreak)
			} else {
				wnd = kcpWndStart
				highStreak = 0
			}
			l.mu.Lock()
			l.rttEwma = rttEwma
			l.mu.Unlock()
			tuneRTT := int(rttEwma + 0.5)
			if tuneRTT > 250 {
				tuneRTT = 250
			}
			applyKCPProfile(sess, tuneRTT, wnd)
			if time.Since(lastLog) > 30*time.Second {
				lastLog = time.Now()
				l.logFn("wbt: KCP tuned rtt=%dms ewma=%dms floor=%dms wnd=%d", rtt, tuneRTT, int(rttFloor+0.5), wnd)
			}
		}
	}
}

func (l *Link) stopLocked() {
	if l.stopCh != nil {
		select {
		case <-l.stopCh:
		default:
			close(l.stopCh)
		}
		l.stopCh = nil
	}
	if l.sess != nil {
		_ = l.sess.Close()
		l.sess = nil
	}
	if l.kc != nil {
		_ = l.kc.Close()
		l.kc = nil
	}
	l.outbound = nil
	l.outboundHi = nil
}

func (l *Link) SetOnCarrierActivity(fn func()) {
	l.mu.Lock()
	l.onCarrierActivity = fn
	l.mu.Unlock()
}

func (l *Link) Session() *kcp.UDPSession {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sess
}

func (l *Link) RTTMs() int {
	l.mu.Lock()
	sess := l.sess
	ewma := l.rttEwma
	l.mu.Unlock()
	if ewma > 0 {
		return int(ewma + 0.5)
	}
	if sess == nil {
		return 0
	}
	if srtt := sess.GetSRTT(); srtt > 0 {
		return int(srtt)
	}
	return int(sess.GetRTO())
}

func (l *Link) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	l.stopLocked()
}

func (l *Link) pumpOutbound(outbound, outboundHi chan []byte, stopCh chan struct{}) {

	var batch []byte
	fromPool := false
	flushTimer := time.NewTimer(kcpVP8FlushWait)
	flushTimer.Stop()

	releaseBatch := func() {
		if fromPool && cap(batch) >= kcpVP8BatchMax {
			bp := batch[:0]
			kcpFramePool.Put(&bp)
		}
		batch = nil
		fromPool = false
	}

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Copy out: SendRaw may retain briefly; pool slice must not alias live data.
		out := make([]byte, len(batch))
		copy(out, batch)
		l.sendFrame(out)
		releaseBatch()
		flushTimer.Stop()
	}

	appendWire := func(pkt []byte) {
		wireLen := 2 + len(pkt)
		if len(batch)+wireLen > kcpVP8BatchMax {
			flush()
		}
		if batch == nil {
			bp := kcpFramePool.Get().(*[]byte)
			batch = (*bp)[:0]
			fromPool = true
		}
		off := len(batch)
		need := off + wireLen
		if cap(batch) < need {
			nb := make([]byte, off, need+64)
			copy(nb, batch)
			if fromPool {
				bp := batch[:0]
				kcpFramePool.Put(&bp)
			}
			batch = nb
			fromPool = false
		}
		batch = batch[:need]
		binary.BigEndian.PutUint16(batch[off:off+2], uint16(len(pkt))) //nolint:gosec // bounded
		copy(batch[off+2:], pkt)
	}

	for {
		// Strict priority: send every pending ACK/control packet before touching
		// bulk data, so a download's ACKs never queue behind an upload backlog in
		// the shared carrier (the upload-burst wedge). ACKs are flushed at once.
		hi := false
	drainHi:
		for {
			select {
			case pkt, ok := <-outboundHi:
				if !ok {
					flush()
					return
				}
				if len(pkt) == 0 || len(pkt) > maxKCPWireFrame {
					continue
				}
				appendWire(pkt)
				hi = true
			default:
				break drainHi
			}
		}
		if hi {
			flush()
		}

		select {
		case <-stopCh:
			flush()
			return
		case <-flushTimer.C:
			flush()
		case pkt, ok := <-outboundHi:
			if !ok {
				flush()
				return
			}
			if len(pkt) == 0 || len(pkt) > maxKCPWireFrame {
				continue
			}
			appendWire(pkt)
			flush()
		case pkt, ok := <-outbound:
			if !ok {
				flush()
				return
			}
			if len(pkt) == 0 || len(pkt) > maxKCPWireFrame {
				continue
			}
			appendWire(pkt)
			q := len(outbound)
			switch {
			case len(batch) >= kcpVP8BatchMax/2, q > 256, len(pkt) < 400:
				flush()
			case q > 64:
				flushTimer.Reset(time.Millisecond)
			default:
				flushTimer.Reset(kcpVP8FlushWait)
			}
		}
	}
}

func (l *Link) sendFrame(frame []byte) {
	if rs, ok := l.tun.(rawSender); ok {
		rs.SendRaw(frame)
		return
	}
	l.tun.SendData(frame)
}

func (l *Link) handleVP8Payload(data []byte) {
	l.mu.Lock()
	kc := l.kc
	reorder := l.reorder
	l.mu.Unlock()
	if kc == nil {
		return
	}
	deliver := func(payload []byte) {
		for len(payload) >= 2 {
			n := int(binary.BigEndian.Uint16(payload[0:2]))
			if n == 0 || 2+n > len(payload) {
				return
			}
			kc.deliver(payload[2 : 2+n])
			payload = payload[2+n:]
		}
	}
	if reorder != nil {
		for _, payload := range reorder.ingest(data) {
			deliver(payload)
		}
	} else {
		deliver(data)
	}
	l.mu.Lock()
	fn := l.onCarrierActivity
	l.mu.Unlock()
	if fn != nil {
		fn()
	}
}

type closedError struct{}

func (closedError) Error() string { return "wbt: closed" }

func netErrClosed() error { return closedError{} }
