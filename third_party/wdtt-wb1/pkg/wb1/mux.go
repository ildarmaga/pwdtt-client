package wb1

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Carrier is a bidirectional packet pipe (LiveKit data topic in production).
type Carrier interface {
	Send(ctx context.Context, payload []byte) error
	Recv(ctx context.Context) ([]byte, error)
}

var (
	errMuxClosed   = errors.New("wb1: mux closed")
	errSendTimeout = errors.New("wb1: send window timeout")
)

type pushStatus int

const (
	pushAdmitted pushStatus = iota
	pushFull
	pushClosed
)

// Mux multiplexes TCP-like streams over WDTT-WB1 frames.
type Mux struct {
	key     []byte
	carrier Carrier
	local   SessionID
	remote  SessionID

	mu       sync.Mutex
	streams  map[uint32]*streamConn
	pending  map[uint32]chan error
	acceptCh chan acceptReq
	nextID   atomic.Uint32
	closed   atomic.Bool

	pings   sync.Map // uint64 nonce -> chan struct{}
	pingSeq atomic.Uint64

	onTraffic func(up, down int64)
	onPeer    func()

	epoch        uint32
	epochSet     bool
	remoteEpoch  uint32
	sendNext     uint32
	sendUnacked  uint32
	sendBuf      map[uint32]*sendSlot
	recvNext     uint32
	recvBuf      map[uint32]Frame
	peerRecvWnd  uint16
	gotPeerWnd   bool
	rto          time.Duration
	srtt         time.Duration
	rttvar       time.Duration
	lastProgress time.Time
	delayAck     *time.Timer
	closeCh      chan struct{}
	sendWait     chan struct{}
	drainCh      chan struct{}
	drainMu      sync.Mutex
}

type acceptReq struct {
	dest string
	conn *streamConn
}

// NewMux builds a mux. Call Run in a goroutine.
func NewMux(key []byte, c Carrier) *Mux {
	var eb [4]byte
	if _, err := rand.Read(eb[:]); err != nil {
		eb[0] = 1
	}
	epoch := binary.BigEndian.Uint32(eb[:])
	if epoch == 0 {
		epoch = 1
	}
	m := &Mux{
		key:          append([]byte(nil), key...),
		carrier:      c,
		streams:      make(map[uint32]*streamConn),
		pending:      make(map[uint32]chan error),
		acceptCh:     make(chan acceptReq, 16),
		epoch:        epoch,
		sendNext:     1,
		sendUnacked:  1,
		recvNext:     1,
		sendBuf:      make(map[uint32]*sendSlot),
		recvBuf:      make(map[uint32]Frame),
		peerRecvWnd:  uint16(ARQWindow),
		rto:          initialRTO,
		lastProgress: time.Now(),
		closeCh:      make(chan struct{}),
		sendWait:     make(chan struct{}, 1),
		drainCh:      make(chan struct{}, 1),
	}
	m.nextID.Store(1)
	return m
}

// SetRoute stamps every send with src=local, dest=remote and drops frames
// that are not addressed to this endpoint pair.
func (m *Mux) SetRoute(local, remote SessionID) {
	m.local = local
	m.remote = remote
}

// SetTrafficHook reports plaintext bytes (up = sent, down = received).
func (m *Mux) SetTrafficHook(fn func(up, down int64)) {
	m.onTraffic = fn
}

// SetPeerHook is called when a remote Open/Data/Ping arrives.
func (m *Mux) SetPeerHook(fn func()) {
	m.onPeer = fn
}

func (m *Mux) notePeer() {
	if m.onPeer != nil {
		m.onPeer()
	}
}

func (m *Mux) addTraffic(up, down int64) {
	if m.onTraffic != nil && (up != 0 || down != 0) {
		m.onTraffic(up, down)
	}
}

// Run reads frames until ctx is done.
func (m *Mux) Run(ctx context.Context) error {
	defer m.Close()
	go m.retransmitLoop(ctx)
	go m.drainLoop(ctx)
	if !m.local.IsZero() && !m.remote.IsZero() {
		_ = m.send(ctx, Frame{Type: TypeHello, StreamID: 0})
	}
	for {
		wire, err := m.carrier.Recv(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		f, err := Unpack(m.key, wire)
		if err != nil {
			continue
		}
		if !m.routeOK(f) {
			continue
		}
		m.handleIncoming(ctx, f)
	}
}

func (m *Mux) routeOK(f Frame) bool {
	if m.local.IsZero() && m.remote.IsZero() {
		return true
	}
	if !m.local.IsZero() && f.Dest != m.local {
		return false
	}
	if !m.remote.IsZero() && f.Src != m.remote {
		return false
	}
	return true
}

func (m *Mux) dispatch(ctx context.Context, f Frame) {
	m.notePeer()
	switch f.Type {
	case TypeHello:
		return
	case TypePing:
		_ = m.send(ctx, Frame{Type: TypePong, StreamID: 0, Payload: f.Payload})
	case TypePong:
		if len(f.Payload) >= 8 {
			id := binary.BigEndian.Uint64(f.Payload[:8])
			if ch, ok := m.pings.LoadAndDelete(id); ok {
				close(ch.(chan struct{}))
			}
		}
	case TypeOpen:
		m.handleOpen(ctx, f)
	case TypeData:
		m.handleData(f)
	case TypeFin, TypeErr:
		m.handleFin(f)
	}
}

func (m *Mux) handleOpen(ctx context.Context, f Frame) {
	dest := string(f.Payload)
	sc := newStream(m, f.StreamID)
	m.mu.Lock()
	if _, ok := m.streams[f.StreamID]; ok {
		m.mu.Unlock()
		sc.remoteClose()
		return
	}
	m.streams[f.StreamID] = sc
	m.mu.Unlock()
	select {
	case m.acceptCh <- acceptReq{dest: dest, conn: sc}:
	case <-ctx.Done():
		sc.Close()
	default:
		m.drop(f.StreamID)
		_ = m.send(ctx, Frame{
			Type: TypeErr, StreamID: f.StreamID, Payload: []byte("accept queue full"),
		})
	}
}

func (m *Mux) handleData(f Frame) {
	m.mu.Lock()
	sc := m.streams[f.StreamID]
	m.mu.Unlock()
	if sc == nil {
		return
	}
	if sc.push(f.Payload) == pushAdmitted {
		m.addTraffic(0, int64(len(f.Payload)))
	}
}

func (m *Mux) handleFin(f Frame) {
	m.mu.Lock()
	sc := m.streams[f.StreamID]
	ch := m.pending[f.StreamID]
	delete(m.pending, f.StreamID)
	m.mu.Unlock()
	if ch != nil {
		select {
		case ch <- fmt.Errorf("wb1: remote: %s", string(f.Payload)):
		default:
		}
	}
	if sc != nil {
		sc.remoteClose()
	}
}

// Accept waits for a remote Open (creator side).
func (m *Mux) Accept(ctx context.Context) (string, net.Conn, error) {
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case req, ok := <-m.acceptCh:
		if !ok {
			return "", nil, errMuxClosed
		}
		return req.dest, req.conn, nil
	}
}

// Dial opens a stream to dest (joiner side). dest is "host:port".
func (m *Mux) Dial(ctx context.Context, dest string) (net.Conn, error) {
	id := m.nextID.Add(1)
	sc := newStream(m, id)
	wait := make(chan error, 1)
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		return nil, errMuxClosed
	}
	m.streams[id] = sc
	m.pending[id] = wait
	m.mu.Unlock()

	if err := m.send(ctx, Frame{Type: TypeOpen, StreamID: id, Payload: []byte(dest)}); err != nil {
		m.drop(id)
		return nil, err
	}
	// Open is fire-and-forget: remote Accept starts copying. No ACK type.
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
	return sc, nil
}

// Ping sends a ping and waits for pong.
func (m *Mux) Ping(ctx context.Context) (time.Duration, error) {
	id := m.pingSeq.Add(1)
	ch := make(chan struct{})
	m.pings.Store(id, ch)
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, id)
	start := time.Now()
	if err := m.send(ctx, Frame{Type: TypePing, StreamID: 0, Payload: payload}); err != nil {
		m.pings.Delete(id)
		return 0, err
	}
	retry := time.NewTimer(minRTO)
	defer retry.Stop()
	retried := false
	for {
		select {
		case <-ctx.Done():
			m.pings.Delete(id)
			return 0, ctx.Err()
		case <-ch:
			return time.Since(start), nil
		case <-retry.C:
			if retried {
				continue
			}
			retried = true
			_ = m.send(ctx, Frame{Type: TypePing, StreamID: 0, Payload: payload})
		}
	}
}

func (m *Mux) send(ctx context.Context, f Frame) error {
	if isReliable(f.Type) {
		return m.sendReliable(ctx, f)
	}
	return m.sendUnsequenced(ctx, f)
}

func (m *Mux) drop(id uint32) {
	m.mu.Lock()
	sc := m.streams[id]
	delete(m.streams, id)
	delete(m.pending, id)
	m.mu.Unlock()
	if sc != nil {
		sc.remoteClose()
	}
}

// Close tears down all streams.
func (m *Mux) Close() {
	if !m.closed.CompareAndSwap(false, true) {
		return
	}
	close(m.closeCh)
	m.stopDelayAck()
	m.wakeSend()
	m.mu.Lock()
	streams := m.streams
	m.streams = map[uint32]*streamConn{}
	m.mu.Unlock()
	for _, sc := range streams {
		sc.remoteClose()
	}
}

type streamConn struct {
	mux    *Mux
	id     uint32
	local  net.Addr
	remote net.Addr

	mu        sync.Mutex
	buf       []byte
	wait      chan struct{}
	closed    bool
	rerr      error
	rdeadline time.Time
	wdeadline time.Time
}

func newStream(m *Mux, id uint32) *streamConn {
	return &streamConn{
		mux:    m,
		id:     id,
		local:  dummyAddr("wb1-local"),
		remote: dummyAddr("wb1-remote"),
		wait:   make(chan struct{}, 1),
	}
}

func (s *streamConn) wakeWait() {
	select {
	case s.wait <- struct{}{}:
	default:
	}
}

func (s *streamConn) push(p []byte) pushStatus {
	if len(p) == 0 {
		return pushAdmitted
	}
	if s.mux.closed.Load() {
		return pushClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return pushClosed
	}
	if len(s.buf)+len(p) > StreamRecvCap {
		return pushFull
	}
	s.buf = append(s.buf, p...)
	s.wakeWait()
	return pushAdmitted
}

func (s *streamConn) remoteClose() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.rerr = io.EOF
	s.wakeWait()
	s.mu.Unlock()
}

func (s *streamConn) Read(p []byte) (int, error) {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		s.mu.Lock()
		if len(s.buf) > 0 {
			n := copy(p, s.buf)
			s.buf = s.buf[n:]
			s.mu.Unlock()
			if n > 0 {
				s.mux.wakeDrain()
			}
			return n, nil
		}
		err := s.rerr
		closed := s.closed
		dl := s.rdeadline
		s.mu.Unlock()
		if closed {
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
		if !dl.IsZero() && !time.Now().Before(dl) {
			return 0, os.ErrDeadlineExceeded
		}
		wait := 30 * time.Second
		if !dl.IsZero() {
			wait = time.Until(dl)
			if wait <= 0 {
				return 0, os.ErrDeadlineExceeded
			}
		}
		if timer == nil {
			timer = time.NewTimer(wait)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(wait)
		}
		select {
		case <-s.wait:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			if s.mux.closed.Load() {
				return 0, errMuxClosed
			}
			if !dl.IsZero() && !time.Now().Before(dl) {
				return 0, os.ErrDeadlineExceeded
			}
		}
	}
}

func (s *streamConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	closed := s.closed
	dl := s.wdeadline
	s.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	ctx := context.Background()
	if !dl.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(context.Background(), dl)
		defer cancel()
	}
	sent := 0
	for sent < len(p) {
		end := sent + MaxPayload
		if end > len(p) {
			end = len(p)
		}
		chunk := p[sent:end]
		if err := s.mux.send(ctx, Frame{Type: TypeData, StreamID: s.id, Payload: chunk}); err != nil {
			if ctx.Err() != nil && !dl.IsZero() {
				return sent, os.ErrDeadlineExceeded
			}
			return sent, err
		}
		sent = end
	}
	return sent, nil
}

func (s *streamConn) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.rerr = net.ErrClosed
	s.wakeWait()
	s.mu.Unlock()
	s.mux.mu.Lock()
	delete(s.mux.streams, s.id)
	s.mux.mu.Unlock()
	finCtx, cancel := context.WithTimeout(context.Background(), arqStallTimeout)
	defer cancel()
	_ = s.mux.send(finCtx, Frame{Type: TypeFin, StreamID: s.id})
	return nil
}

func (s *streamConn) LocalAddr() net.Addr  { return s.local }
func (s *streamConn) RemoteAddr() net.Addr { return s.remote }

func (s *streamConn) SetDeadline(t time.Time) error {
	_ = s.SetReadDeadline(t)
	return s.SetWriteDeadline(t)
}

func (s *streamConn) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	s.rdeadline = t
	s.mu.Unlock()
	s.wakeWait()
	return nil
}

func (s *streamConn) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.wdeadline = t
	s.mu.Unlock()
	return nil
}

type dummyAddr string

func (d dummyAddr) Network() string { return "wb1" }
func (d dummyAddr) String() string  { return string(d) }
