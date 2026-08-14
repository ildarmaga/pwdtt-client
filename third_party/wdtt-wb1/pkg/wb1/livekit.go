package wb1

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	DataTopic            = "wdtt-wb1"
	CreatorName          = "WDTT"
	CreatorBeaconPayload = "wdtt-wb1-creator"
	MaxJoiners           = 32
	joinerIdleTimeout    = 30 * time.Second
	joinerReapInterval   = 5 * time.Second
	vp8Keepalive         = 33 * time.Millisecond
	vp8SampleDur         = 33 * time.Millisecond
	vp8VideoWidth        = 640
	vp8VideoHeight       = 360
)

// 16×16 VP8 keyframe — keeps the WB call alive (platform wants a publisher).
var vp8Keyframe = []byte{
	0x90, 0x00, 0x00, 0x9d, 0x01, 0x2a, 0x10, 0x00, 0x10, 0x00,
	0x02, 0x47, 0x08, 0x85, 0x85, 0x88, 0x85, 0x84, 0x88, 0x02,
	0x02, 0x00, 0x0c, 0x0d, 0x60, 0x00, 0xfe, 0xfc, 0x5a, 0x00, 0x00,
}

type pendingJoiner struct {
	sid SessionID
	c   Carrier
}

// RoomSession is a LiveKit room: one call, many joiners (like VK).
type RoomSession struct {
	room     *lksdk.Room
	done     chan struct{}
	cancel   context.CancelFunc
	localSID SessionID

	videoMu sync.Mutex
	video   *lksdk.LocalTrack

	vp8N     atomic.Uint64
	vp8Stamp atomic.Int64
	vp8FPS   atomic.Int64

	mu             sync.Mutex
	peers          map[string]*Peer
	onPeer         func(*Peer)
	pending        []*Peer
	joiners        map[SessionID]*sidPipe
	onJoiner       func(SessionID, Carrier)
	onJoinerGone   func(SessionID)
	pendingJoiners []pendingJoiner
	key            []byte
	beaconID       string
	creatorSID     SessionID
	incoming       chan []byte
	isCreator      atomic.Uint32
	loggedV1       atomic.Uint32
}

// Peer is one remote LiveKit participant (presence/logging only).
type Peer struct {
	Identity  string
	Name      string
	Meta      string
	hash      SessionID
	recv      chan []byte
	closed    chan struct{}
	sess      *RoomSession
	closeOnce sync.Once
	spoke     atomic.Uint32
	beacon    atomic.Uint32
}

func (p *Peer) push(b []byte) {
	if len(b) == 0 {
		return
	}
	p.spoke.Store(1)
	if p.sess != nil {
		p.sess.ingest(b)
		return
	}
	cp := append([]byte(nil), b...)
	select {
	case p.recv <- cp:
	default:
		select {
		case <-p.recv:
		default:
		}
		select {
		case p.recv <- cp:
		default:
		}
	}
}

func (p *Peer) Close() {
	p.closeOnce.Do(func() { close(p.closed) })
}

// Send is leftover Carrier compat: mux payloads go VP8 only (no data-topic duplicate).
func (p *Peer) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.sess == nil {
		return errMuxClosed
	}
	_, dest, _, ok := PeekRoute(payload)
	if !ok {
		dest = p.hash
	}
	return p.sess.publishWire(dest, payload)
}

// Recv implements Carrier for tests that still push onto Peer.recv.
func (p *Peer) Recv(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.closed:
		return nil, context.Canceled
	case <-p.sess.done:
		return nil, context.Canceled
	case b := <-p.recv:
		return b, nil
	}
}

type sidPipe struct {
	sess     *RoomSession
	remote   SessionID
	recv     chan []byte
	closed   chan struct{}
	once     sync.Once
	lastSeen atomic.Int64
}

func newSIDPipe(s *RoomSession, remote SessionID) *sidPipe {
	p := &sidPipe{
		sess:   s,
		remote: remote,
		recv:   make(chan []byte, 1024),
		closed: make(chan struct{}),
	}
	p.touch()
	return p
}

func (p *sidPipe) touch() {
	p.lastSeen.Store(time.Now().UnixNano())
}

func (p *sidPipe) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.sess == nil {
		return errMuxClosed
	}
	_, dest, _, ok := PeekRoute(payload)
	if !ok {
		dest = p.remote
	}
	return p.sess.publishWire(dest, payload)
}

func (p *sidPipe) Recv(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.closed:
		return nil, context.Canceled
	case <-p.sess.done:
		return nil, context.Canceled
	case b := <-p.recv:
		return b, nil
	}
}

func (p *sidPipe) push(b []byte) {
	if len(b) == 0 {
		return
	}
	p.touch()
	cp := append([]byte(nil), b...)
	select {
	case p.recv <- cp:
	default:
		select {
		case <-p.recv:
		default:
		}
		select {
		case p.recv <- cp:
		default:
		}
	}
}

func (p *sidPipe) Close() {
	p.once.Do(func() { close(p.closed) })
}

// JoinerCarrier is the joiner mux pipe: VP8 send, SID-filtered recv.
func (s *RoomSession) JoinerCarrier() Carrier {
	return &joinerCarrier{s: s}
}

type joinerCarrier struct{ s *RoomSession }

func (c *joinerCarrier) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.s == nil {
		return errMuxClosed
	}
	dest := c.s.CreatorSID()
	if _, envDest, _, ok := PeekRoute(payload); ok && !envDest.IsZero() {
		dest = envDest
	}
	return c.s.publishWire(dest, payload)
}

func (c *joinerCarrier) Recv(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.s.done:
		return nil, context.Canceled
	case b := <-c.s.incoming:
		return b, nil
	}
}

// ConnectRoom joins LiveKit, publishes VP8, and accepts N remote peers.
func ConnectRoom(parent context.Context, serverURL, roomToken string) (*RoomSession, error) {
	ctx, cancel := context.WithCancel(parent)
	sid, err := NewSessionID()
	if err != nil {
		cancel()
		return nil, err
	}
	s := &RoomSession{
		done:     make(chan struct{}),
		cancel:   cancel,
		localSID: sid,
		peers:    make(map[string]*Peer),
		joiners:  make(map[SessionID]*sidPipe),
		incoming: make(chan []byte, 1024),
	}
	cb := &lksdk.RoomCallback{
		OnDisconnected: func() {
			s.cancel()
		},
		OnDisconnectedWithReason: func(_ lksdk.DisconnectionReason) {
			s.cancel()
		},
		OnParticipantConnected: func(rp *lksdk.RemoteParticipant) {
			s.ensurePeer(rp.Identity(), rp.Name(), rp.Metadata())
		},
		OnParticipantDisconnected: func(rp *lksdk.RemoteParticipant) {
			s.dropPeer(rp.Identity())
		},
		ParticipantCallback: lksdk.ParticipantCallback{
			OnDataPacket: func(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
				ud, ok := data.(*lksdk.UserDataPacket)
				if !ok {
					return
				}
				topic := ud.Topic
				if topic == "" {
					topic = params.Topic
				}
				if topic != "" && topic != DataTopic {
					return
				}
				from := params.SenderIdentity
				if from == "" && params.Sender != nil {
					from = params.Sender.Identity()
				}
				s.dispatch(from, ud.Payload)
			},
			OnTrackSubscribed: func(track *webrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				if track.Kind() != webrtc.RTPCodecTypeVideo {
					return
				}
				p := s.ensurePeer(rp.Identity(), rp.Name(), rp.Metadata())
				go s.readTrack(track, p)
			},
			OnMetadataChanged: func(_ string, part lksdk.Participant) {
				rp, ok := part.(*lksdk.RemoteParticipant)
				if !ok {
					return
				}
				p := s.ensurePeer(rp.Identity(), rp.Name(), rp.Metadata())
				if p != nil {
					p.Name = rp.Name()
					p.Meta = rp.Metadata()
				}
			},
		},
	}
	room, err := lksdk.ConnectToRoomWithToken(serverURL, roomToken, cb, lksdk.WithAutoSubscribe(true))
	if err != nil {
		cancel()
		return nil, err
	}
	s.room = room
	if err := s.publishDummyVideo(ctx); err != nil {
		room.Disconnect()
		cancel()
		return nil, err
	}
	for _, rp := range room.GetRemoteParticipants() {
		s.ensurePeer(rp.Identity(), rp.Name(), rp.Metadata())
	}
	go func() {
		<-ctx.Done()
		room.Disconnect()
		close(s.done)
	}()
	go s.reapJoiners(ctx)
	return s, nil
}

func (s *RoomSession) publishDummyVideo(ctx context.Context) error {
	track, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeVP8,
		ClockRate: 90000,
	})
	if err != nil {
		return err
	}
	if _, err := s.room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name:        "camera",
		Source:      livekit.TrackSource_CAMERA,
		VideoWidth:  vp8VideoWidth,
		VideoHeight: vp8VideoHeight,
	}); err != nil {
		return err
	}
	s.videoMu.Lock()
	s.video = track
	s.videoMu.Unlock()
	go func() {
		tick := time.NewTicker(vp8Keepalive)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s.videoMu.Lock()
				v := s.video
				s.videoMu.Unlock()
				if v != nil {
					_ = v.WriteSample(media.Sample{Data: vp8Keyframe, Duration: vp8SampleDur}, nil)
					s.noteVP8()
				}
			}
		}
	}()
	return nil
}

func (s *RoomSession) writeVP8(dest SessionID, frame []byte) error {
	s.videoMu.Lock()
	v := s.video
	s.videoMu.Unlock()
	if v == nil {
		return errMuxClosed
	}
	err := v.WriteSample(media.Sample{Data: WrapVP8(dest, frame), Duration: time.Millisecond}, nil)
	if err == nil {
		s.noteVP8()
	}
	return err
}

func (s *RoomSession) publishWire(dest SessionID, wire []byte) error {
	typ, envDest, _, ok := PeekRoute(wire)
	if !ok || envDest != dest {
		return errBadLength
	}
	useData, useVP8 := publishPlan(dest, typ)
	var dataErr error
	if useData {
		if s.room == nil {
			dataErr = errMuxClosed
		} else {
			pkt := lksdk.UserData(wire)
			pkt.Topic = DataTopic
			dataErr = s.room.LocalParticipant.PublishDataPacket(
				pkt,
				lksdk.WithDataPublishReliable(true),
				lksdk.WithDataPublishTopic(DataTopic),
			)
		}
	}
	if useVP8 {
		if err := s.writeVP8(dest, wire); err != nil {
			return err
		}
	}
	return dataErr
}

func (s *RoomSession) noteVP8() {
	n := s.vp8N.Add(1)
	now := time.Now().UnixNano()
	start := s.vp8Stamp.Load()
	if start == 0 {
		s.vp8Stamp.Store(now)
		return
	}
	if now-start >= int64(time.Second) {
		s.vp8FPS.Store(int64(n))
		s.vp8N.Store(0)
		s.vp8Stamp.Store(now)
	}
}

// VP8FPS is samples written in the last 1s window (keepalive + data).
func (s *RoomSession) VP8FPS() int64 {
	fps := s.vp8FPS.Load()
	if fps <= 0 {
		n := s.vp8N.Load()
		if n > 0 {
			return int64(n)
		}
		return 1
	}
	return fps
}

func (s *RoomSession) readTrack(track *webrtc.TrackRemote, p *Peer) {
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		if pkt == nil {
			continue
		}
		dest, frame, ok := UnwrapVP8(pkt.Payload)
		if !ok {
			continue
		}
		if !DestForMe(s.localSID, dest) {
			continue
		}
		if p != nil {
			p.spoke.Store(1)
		}
		s.ingest(frame)
	}
}

func (s *RoomSession) dispatch(from string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	if from != "" {
		_ = s.ensurePeer(from, "", "")
	}
	s.ingest(payload)
}

func (s *RoomSession) ingest(wire []byte) {
	if len(wire) == 0 {
		return
	}
	s.mu.Lock()
	key := s.key
	s.mu.Unlock()
	if isCreatorBeacon(key, wire) {
		if id, sid, ok := creatorFromBeacon(key, wire); ok {
			s.noteBeaconCreator(id, sid)
		}
		return
	}
	if len(wire) >= MagicSize && bytesEqual(wire[:MagicSize], MagicV1[:]) {
		if s.loggedV1.CompareAndSwap(0, 1) {
			log.Printf("wb1: dropped v1 frame (need matching v2 panel+client)")
		}
		return
	}
	f, err := Unpack(key, wire)
	if err != nil {
		return
	}
	if f.Dest.IsZero() || f.Src.IsZero() {
		return
	}
	if f.Dest != s.localSID {
		return
	}
	if s.isCreator.Load() != 0 {
		s.deliverToJoiner(f.Src, f.Type, wire)
		return
	}
	s.mu.Lock()
	want := s.creatorSID
	s.mu.Unlock()
	if want.IsZero() || f.Src != want {
		return
	}
	cp := append([]byte(nil), wire...)
	select {
	case s.incoming <- cp:
	default:
		select {
		case <-s.incoming:
		default:
		}
		select {
		case s.incoming <- cp:
		default:
		}
	}
}

func (s *RoomSession) deliverToJoiner(src SessionID, typ byte, wire []byte) {
	s.mu.Lock()
	p, ok := s.joiners[src]
	if ok {
		s.mu.Unlock()
		p.push(wire)
		return
	}
	if typ != TypeHello && typ != TypeOpen && typ != TypeData && typ != TypePing {
		s.mu.Unlock()
		return
	}
	if len(s.joiners) >= MaxJoiners {
		s.mu.Unlock()
		log.Printf("wb1: joiner cap %d, dropping sid=%s", MaxJoiners, src.Hex())
		return
	}
	p = newSIDPipe(s, src)
	s.joiners[src] = p
	fn := s.onJoiner
	if fn == nil {
		s.pendingJoiners = append(s.pendingJoiners, pendingJoiner{sid: src, c: p})
	}
	s.mu.Unlock()
	p.push(wire)
	if fn != nil {
		fn(src, p)
	}
}

func (s *RoomSession) reapJoiners(ctx context.Context) {
	ticker := time.NewTicker(joinerReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.reapIdleJoiners(now, joinerIdleTimeout)
		}
	}
}

func (s *RoomSession) reapIdleJoiners(now time.Time, idle time.Duration) {
	cutoff := now.Add(-idle).UnixNano()
	var expired []struct {
		sid SessionID
		p   *sidPipe
	}
	s.mu.Lock()
	for sid, p := range s.joiners {
		if p != nil && p.lastSeen.Load() <= cutoff {
			delete(s.joiners, sid)
			expired = append(expired, struct {
				sid SessionID
				p   *sidPipe
			}{sid: sid, p: p})
		}
	}
	gone := s.onJoinerGone
	s.mu.Unlock()
	for _, item := range expired {
		item.p.Close()
		if gone != nil {
			gone(item.sid)
		}
	}
}

func (s *RoomSession) ensurePeer(identity, name, meta string) *Peer {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil
	}
	s.mu.Lock()
	p, ok := s.peers[identity]
	if !ok {
		p = &Peer{
			Identity: identity,
			Name:     name,
			Meta:     meta,
			hash:     IdentityHash(identity),
			recv:     make(chan []byte, 1024),
			closed:   make(chan struct{}),
			sess:     s,
		}
		s.peers[identity] = p
		fn := s.onPeer
		s.mu.Unlock()
		if fn != nil {
			fn(p)
		} else {
			s.mu.Lock()
			s.pending = append(s.pending, p)
			s.mu.Unlock()
		}
		return p
	}
	if name != "" {
		p.Name = name
	}
	if meta != "" {
		p.Meta = meta
	}
	s.mu.Unlock()
	return p
}

// AnnounceCreator marks this participant as the panel WDTT-WB1 creator.
func (s *RoomSession) AnnounceCreator() {
	if s.room == nil {
		return
	}
	s.isCreator.Store(1)
	s.room.LocalParticipant.SetName(CreatorName)
	s.room.LocalParticipant.SetMetadata("wdtt-wb1-creator")
}

func (s *RoomSession) dropPeer(identity string) {
	s.mu.Lock()
	p := s.peers[identity]
	delete(s.peers, identity)
	s.mu.Unlock()
	if p != nil {
		p.Close()
	}
}

// SetOnPeer is called for every remote participant (presence/logging only).
func (s *RoomSession) SetOnPeer(fn func(*Peer)) {
	s.mu.Lock()
	s.onPeer = fn
	pending := append([]*Peer(nil), s.pending...)
	s.pending = nil
	var all []*Peer
	for _, p := range s.peers {
		all = append(all, p)
	}
	s.mu.Unlock()
	seen := map[string]bool{}
	deliver := func(p *Peer) {
		if p == nil || seen[p.Identity] {
			return
		}
		seen[p.Identity] = true
		fn(p)
	}
	for _, p := range pending {
		deliver(p)
	}
	for _, p := range all {
		deliver(p)
	}
}

// SetOnJoiner is called once per authenticated joiner SID (lazy mux).
func (s *RoomSession) SetOnJoiner(fn func(sid SessionID, c Carrier)) {
	s.mu.Lock()
	s.onJoiner = fn
	pending := append([]pendingJoiner(nil), s.pendingJoiners...)
	s.pendingJoiners = nil
	s.mu.Unlock()
	for _, p := range pending {
		fn(p.sid, p.c)
	}
}

// SetOnJoinerGone is called after an inactive SID is removed.
func (s *RoomSession) SetOnJoinerGone(fn func(SessionID)) {
	s.mu.Lock()
	s.onJoinerGone = fn
	s.mu.Unlock()
}

// StartCreatorBeacon broadcasts a WDTT-WB1 ping so joiners find us without
// waiting for LiveKit SetName (WB often leaves remote Name empty).
func (s *RoomSession) StartCreatorBeacon(ctx context.Context, key []byte) {
	go func() {
		tick := time.NewTicker(400 * time.Millisecond)
		defer tick.Stop()
		send := func() {
			if s.room == nil {
				return
			}
			id := strings.TrimSpace(s.room.LocalParticipant.Identity())
			payload := FormatBeaconPayload(id, s.localSID)
			wire, err := Pack(key, Frame{Type: TypePing, Dest: SessionID{}, Src: s.localSID, Payload: []byte(payload)})
			if err != nil {
				return
			}
			var zero SessionID
			_ = s.publishWire(zero, wire)
		}
		send()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			case <-tick.C:
				send()
			}
		}
	}()
}

// WaitCreator blocks until the panel creator SID is known from a v2 beacon.
func (s *RoomSession) WaitCreator(ctx context.Context) (*Peer, error) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if !s.CreatorSID().IsZero() {
			if p := s.creatorPeer(); p != nil {
				return p, nil
			}
			s.mu.Lock()
			id := s.beaconID
			s.mu.Unlock()
			return &Peer{Identity: id, Name: CreatorName, sess: s}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
			return nil, context.Canceled
		case <-tick.C:
		}
	}
}

func (s *RoomSession) creatorPeer() *Peer {
	s.mu.Lock()
	id := strings.TrimSpace(s.beaconID)
	if id != "" {
		if p := s.peers[id]; p != nil {
			s.mu.Unlock()
			return p
		}
	}
	s.mu.Unlock()
	return nil
}

// LocalSID is this endpoint's random session id.
func (s *RoomSession) LocalSID() SessionID {
	return s.localSID
}

// CreatorSID is the panel creator endpoint id parsed from the v2 beacon.
func (s *RoomSession) CreatorSID() SessionID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creatorSID
}

// IsLeftoverCreator reports a stale LiveKit participant that still looks like
// the panel creator (name WDTT / metadata) after the real creator reconnected.
func IsLeftoverCreator(p *Peer) bool {
	if p == nil {
		return false
	}
	if strings.Contains(p.Meta, "wdtt-wb1-creator") {
		return true
	}
	n := strings.ToUpper(strings.TrimSpace(p.Name))
	return n == CreatorName || strings.HasPrefix(n, CreatorName)
}

func (s *RoomSession) noteBeaconCreator(id string, sid SessionID) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if !s.creatorSID.IsZero() {
		s.mu.Unlock()
		return
	}
	if id != "" {
		s.beaconID = id
	}
	if !sid.IsZero() {
		s.creatorSID = sid
	}
	s.mu.Unlock()
	if id != "" {
		_ = s.ensurePeer(id, CreatorName, "wdtt-wb1-creator")
	}
}

func isCreatorBeacon(key, wire []byte) bool {
	if len(key) == 0 || len(wire) == 0 {
		return false
	}
	f, err := Unpack(key, wire)
	if err != nil || f.Type != TypePing {
		return false
	}
	return isBeaconPlain(f.Payload)
}

func creatorFromBeacon(key, wire []byte) (string, SessionID, bool) {
	if len(key) == 0 || len(wire) == 0 {
		return "", SessionID{}, false
	}
	f, err := Unpack(key, wire)
	if err != nil || f.Type != TypePing {
		return "", SessionID{}, false
	}
	return ParseBeaconPayload(string(f.Payload))
}

func creatorIDFromBeacon(key, wire []byte) string {
	id, _, ok := creatorFromBeacon(key, wire)
	if !ok {
		return ""
	}
	return id
}

// FormatBeaconPayload is wdtt-wb1-creator|<livekit-id>|<hex creatorSID>.
func FormatBeaconPayload(livekitID string, sid SessionID) string {
	return CreatorBeaconPayload + "|" + strings.TrimSpace(livekitID) + "|" + sid.Hex()
}

// ParseBeaconPayload reads a v2 creator beacon. Two-part v1 beacons return ok=false.
func ParseBeaconPayload(p string) (livekitID string, sid SessionID, ok bool) {
	prefix := CreatorBeaconPayload + "|"
	if !strings.HasPrefix(p, prefix) {
		return "", sid, false
	}
	rest := p[len(prefix):]
	id, hexSID, found := strings.Cut(rest, "|")
	if !found {
		return "", sid, false
	}
	sid, err := ParseSessionIDHex(hexSID)
	if err != nil || sid.IsZero() {
		return "", sid, false
	}
	return strings.TrimSpace(id), sid, true
}

// SetCryptoKey lets WaitCreator recognize the panel beacon among leftover joiners.
func (s *RoomSession) SetCryptoKey(key []byte) {
	s.mu.Lock()
	s.key = append([]byte(nil), key...)
	s.mu.Unlock()
}

// Close leaves the room.
func (s *RoomSession) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.room != nil {
		s.room.Disconnect()
	}
}

// Done is closed after disconnect.
func (s *RoomSession) Done() <-chan struct{} { return s.done }

// PeerLabels returns LiveKit identity/name of remote participants.
func (s *RoomSession) PeerLabels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.peers)*2)
	for _, p := range s.peers {
		if p == nil {
			continue
		}
		if p.Identity != "" {
			out = append(out, p.Identity)
		}
		if p.Name != "" {
			out = append(out, p.Name)
		}
	}
	return out
}
