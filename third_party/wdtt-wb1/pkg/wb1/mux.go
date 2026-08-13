package wb1

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Carrier is a bidirectional packet pipe (LiveKit data topic in production).
type Carrier interface {
	Send(ctx context.Context, payload []byte) error
	Recv(ctx context.Context) ([]byte, error)
}

var errMuxClosed = errors.New("wb1: mux closed")

// Mux multiplexes TCP-like streams over WDTT-WB1 frames.
type Mux struct {
	key     []byte
	carrier Carrier

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
}

type acceptReq struct {
	dest string
	conn *streamConn
}

// NewMux builds a mux. Call Run in a goroutine.
func NewMux(key []byte, c Carrier) *Mux {
	m := &Mux{
		key:      append([]byte(nil), key...),
		carrier:  c,
		streams:  make(map[uint32]*streamConn),
		pending:  make(map[uint32]chan error),
		acceptCh: make(chan acceptReq, 16),
	}
	m.nextID.Store(1)
	return m
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
		m.dispatch(ctx, f)
	}
}

func (m *Mux) dispatch(ctx context.Context, f Frame) {
	m.notePeer()
	switch f.Type {
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
		m.handleOpen(f)
	case TypeData:
		m.handleData(f)
	case TypeFin, TypeErr:
		m.handleFin(f)
	}
}

func (m *Mux) handleOpen(f Frame) {
	dest := string(f.Payload)
	sc := newStream(m, f.StreamID)
	m.mu.Lock()
	if _, ok := m.streams[f.StreamID]; ok {
		m.mu.Unlock()
		return
	}
	m.streams[f.StreamID] = sc
	m.mu.Unlock()
	select {
	case m.acceptCh <- acceptReq{dest: dest, conn: sc}:
	default:
		sc.Close()
	}
}

func (m *Mux) handleData(f Frame) {
	m.mu.Lock()
	sc := m.streams[f.StreamID]
	m.mu.Unlock()
	if sc == nil {
		return
	}
	m.addTraffic(0, int64(len(f.Payload)))
	sc.push(f.Payload)
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
	select {
	case <-ctx.Done():
		m.pings.Delete(id)
		return 0, ctx.Err()
	case <-ch:
		return time.Since(start), nil
	}
}

func (m *Mux) send(ctx context.Context, f Frame) error {
	if m.closed.Load() {
		return errMuxClosed
	}
	wire, err := Pack(m.key, f)
	if err != nil {
		return err
	}
	if f.Type == TypeData {
		m.addTraffic(int64(len(f.Payload)), 0)
	}
	return m.carrier.Send(ctx, wire)
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

	mu     sync.Mutex
	buf    []byte
	wait   chan struct{}
	closed bool
	rerr   error
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

func (s *streamConn) push(p []byte) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.buf = append(s.buf, p...)
	select {
	case s.wait <- struct{}{}:
	default:
	}
	s.mu.Unlock()
}

func (s *streamConn) remoteClose() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.rerr = io.EOF
	select {
	case s.wait <- struct{}{}:
	default:
	}
	s.mu.Unlock()
}

func (s *streamConn) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.buf) > 0 {
			n := copy(p, s.buf)
			s.buf = s.buf[n:]
			s.mu.Unlock()
			return n, nil
		}
		err := s.rerr
		closed := s.closed
		s.mu.Unlock()
		if closed {
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
		select {
		case <-s.wait:
		case <-time.After(30 * time.Second):
			if s.mux.closed.Load() {
				return 0, errMuxClosed
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
	s.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	sent := 0
	ctx := context.Background()
	for sent < len(p) {
		end := sent + MaxPayload
		if end > len(p) {
			end = len(p)
		}
		chunk := p[sent:end]
		if err := s.mux.send(ctx, Frame{Type: TypeData, StreamID: s.id, Payload: chunk}); err != nil {
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
	select {
	case s.wait <- struct{}{}:
	default:
	}
	s.mu.Unlock()
	_ = s.mux.send(context.Background(), Frame{Type: TypeFin, StreamID: s.id})
	s.mux.mu.Lock()
	delete(s.mux.streams, s.id)
	s.mux.mu.Unlock()
	return nil
}

func (s *streamConn) LocalAddr() net.Addr                { return s.local }
func (s *streamConn) RemoteAddr() net.Addr               { return s.remote }
func (s *streamConn) SetDeadline(time.Time) error        { return nil }
func (s *streamConn) SetReadDeadline(time.Time) error    { return nil }
func (s *streamConn) SetWriteDeadline(time.Time) error   { return nil }

type dummyAddr string

func (d dummyAddr) Network() string { return "wb1" }
func (d dummyAddr) String() string  { return string(d) }
