package wbtunnel

import (
	"encoding/binary"
	"net"
	"sync"
	"time"
)

// kcp-go wire header (little-endian): conv(4) cmd(1) frg(1) wnd(2) ts(4) sn(4)
// una(4) len(4) = 24 bytes, then `len` bytes of payload for a data segment.
const (
	ikcpOverhead = 24
	ikcpCmdPush  = 81 // IKCP_CMD_PUSH — carries payload (bulk data)
)

func fakeUDPAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}

// kcpPacketIsControl reports whether a serialized KCP output packet carries NO
// data payload — i.e. it is purely ACK / window-probe / window-update. Such
// packets are latency-critical and tiny: on a carrier shared by both directions
// they MUST overtake bulk upload data, otherwise a download's ACKs queue behind
// megabytes of upload segments in the single outbound FIFO and the download
// stalls to 0 B/s (the observed upload-burst wedge). A packet may concatenate
// several segments; it is control only if none is a data-bearing PUSH.
func kcpPacketIsControl(p []byte) bool {
	if len(p) < ikcpOverhead {
		return true // too small to be a data segment → treat as urgent
	}
	for len(p) >= ikcpOverhead {
		cmd := p[4]
		segLen := int(binary.LittleEndian.Uint32(p[20:24]))
		if segLen < 0 || ikcpOverhead+segLen > len(p) {
			return false // unparseable → be safe, send on the bulk lane
		}
		if cmd == ikcpCmdPush && segLen > 0 {
			return false
		}
		p = p[ikcpOverhead+segLen:]
	}
	return true
}

// kcpConn bridges kcp-go on top of the VP8 byte carrier.
type kcpConn struct {
	outLo     chan<- []byte // bulk data (PUSH segments)
	outHi     chan<- []byte // control: ACK / window probes — drained first
	in        chan []byte
	closed    chan struct{}
	closeOnce sync.Once

	mu        sync.Mutex
	rDeadline time.Time
	wDeadline time.Time
}

func newKCPConn(outLo, outHi chan<- []byte, inboundCap int) *kcpConn {
	if inboundCap <= 0 {
		inboundCap = 1024
	}
	return &kcpConn{
		outLo:  outLo,
		outHi:  outHi,
		in:     make(chan []byte, inboundCap),
		closed: make(chan struct{}),
	}
}

func (c *kcpConn) deliver(payload []byte) {
	cp := make([]byte, len(payload))
	copy(cp, payload)
	// Non-blocking, like a real UDP socket. This runs on the VP8 receive callback
	// (the carrier read loop); blocking it here — as the old 2s backpressure did —
	// stalls the ENTIRE carrier read path (all tracks, both directions), which is
	// far worse than a dropped segment. KCP is reliable, so a drop is just carrier
	// loss and is retransmitted. Keep c.in generous so drops only happen under a
	// genuine overload, not normal jitter.
	select {
	case c.in <- cp:
	case <-c.closed:
	default:
	}
}

func (c *kcpConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
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

	select {
	case msg := <-c.in:
		n := copy(p, msg)
		return n, fakeUDPAddr(), nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case <-timerC:
		return 0, nil, timeoutError{}
	}
}

func (c *kcpConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}

	cp := make([]byte, len(p))
	copy(cp, p)

	// Route ACK/control onto the priority lane so it overtakes bulk upload data.
	out := c.outLo
	if kcpPacketIsControl(p) {
		out = c.outHi
	}

	// Non-blocking, like a real UDP socket. kcp-go serializes ALL output — data
	// AND ACKs — through a single tx goroutine that calls this WriteTo in order;
	// blocking here on a full queue froze that goroutine and stalled EVERY packet
	// (ACKs included), so an upload burst wedged both directions and RTT ran away
	// to 20s+ (OpenStream: timeout) with no recovery. Dropping instead is exactly
	// how a UDP socket behaves under a full send buffer; KCP retransmits. Report
	// success so KCP schedules its own RTO-driven resend rather than tearing down.
	select {
	case out <- cp:
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	return len(p), nil
}

func (c *kcpConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *kcpConn) LocalAddr() net.Addr { return fakeUDPAddr() }

func (c *kcpConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	_ = c.SetWriteDeadline(t)
	return nil
}

func (c *kcpConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rDeadline = t
	c.mu.Unlock()
	return nil
}

func (c *kcpConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.wDeadline = t
	c.mu.Unlock()
	return nil
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
