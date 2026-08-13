package wb1

import (
	"context"
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
	DataTopic      = "wdtt-wb1"
	CreatorName    = "WDTT"
	vp8Keepalive   = 33 * time.Millisecond
	vp8SampleDur   = 33 * time.Millisecond
	vp8VideoWidth  = 640
	vp8VideoHeight = 360
)

// 16×16 VP8 keyframe — keeps the WB call alive (platform wants a publisher).
var vp8Keyframe = []byte{
	0x90, 0x00, 0x00, 0x9d, 0x01, 0x2a, 0x10, 0x00, 0x10, 0x00,
	0x02, 0x47, 0x08, 0x85, 0x85, 0x88, 0x85, 0x84, 0x88, 0x02,
	0x02, 0x00, 0x0c, 0x0d, 0x60, 0x00, 0xfe, 0xfc, 0x5a, 0x00, 0x00,
}

// RoomSession is a LiveKit room: one call, many joiners (like VK).
type RoomSession struct {
	room   *lksdk.Room
	done   chan struct{}
	cancel context.CancelFunc
	me     [8]byte

	videoMu sync.Mutex
	video   *lksdk.LocalTrack

	vp8N     atomic.Uint64
	vp8Stamp atomic.Int64
	vp8FPS   atomic.Int64

	mu      sync.Mutex
	peers   map[string]*Peer
	onPeer  func(*Peer)
	pending []*Peer
}

// Peer is one remote participant with its own WDTT-WB1 carrier.
type Peer struct {
	Identity  string
	Name      string
	Meta      string
	hash      [8]byte
	recv      chan []byte
	closed    chan struct{}
	sess      *RoomSession
	closeOnce sync.Once
	spoke     atomic.Uint32
}

func (p *Peer) push(b []byte) {
	if len(b) == 0 {
		return
	}
	p.spoke.Store(1)
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

// Send implements Carrier. Mux frames go on the LiveKit data topic.
// VP8 is a 30fps keepalive plus fallback if data publish fails — stuffing every
// mux chunk into WriteSample(Duration=33ms) capped SOCKS at ~1 MB/s.
func (p *Peer) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.sess == nil || p.sess.room == nil {
		return errMuxClosed
	}
	pkt := lksdk.UserData(payload)
	pkt.Topic = DataTopic
	err := p.sess.room.LocalParticipant.PublishDataPacket(
		pkt,
		lksdk.WithDataPublishReliable(true),
		lksdk.WithDataPublishTopic(DataTopic),
		lksdk.WithDataPublishDestination([]string{p.Identity}),
	)
	if err != nil {
		return p.sess.writeVP8(p.hash, payload)
	}
	return nil
}

// Recv implements Carrier.
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

// ConnectRoom joins LiveKit, publishes VP8, and accepts N remote peers.
func ConnectRoom(parent context.Context, serverURL, roomToken string) (*RoomSession, error) {
	ctx, cancel := context.WithCancel(parent)
	s := &RoomSession{
		done:   make(chan struct{}),
		cancel: cancel,
		peers:  make(map[string]*Peer),
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
	s.me = IdentityHash(room.LocalParticipant.Identity())
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

func (s *RoomSession) writeVP8(dest [8]byte, frame []byte) error {
	s.videoMu.Lock()
	v := s.video
	s.videoMu.Unlock()
	if v == nil {
		return errMuxClosed
	}
	err := v.WriteSample(media.Sample{Data: WrapVP8(dest, frame), Duration: vp8SampleDur}, nil)
	if err == nil {
		s.noteVP8()
	}
	return err
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
		if !DestForMe(s.me, dest) {
			continue
		}
		p.push(frame)
	}
}

func (s *RoomSession) dispatch(from string, payload []byte) {
	if from == "" || len(payload) == 0 {
		return
	}
	p := s.ensurePeer(from, "", "")
	if p != nil {
		p.push(payload)
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

// SetOnPeer is called for every remote participant (existing and new).
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
			wire, err := Pack(key, Frame{Type: TypePing, StreamID: 0, Payload: []byte("wdtt-wb1-creator")})
			if err != nil {
				return
			}
			pkt := lksdk.UserData(wire)
			pkt.Topic = DataTopic
			_ = s.room.LocalParticipant.PublishDataPacket(
				pkt,
				lksdk.WithDataPublishReliable(true),
				lksdk.WithDataPublishTopic(DataTopic),
			)
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

// WaitCreator blocks until the panel creator is identified (name, metadata, or WB1 traffic).
func (s *RoomSession) WaitCreator(ctx context.Context) (*Peer, error) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if p := s.creatorPeer(); p != nil {
			return p, nil
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
	defer s.mu.Unlock()
	var spoke *Peer
	for _, p := range s.peers {
		if p == nil {
			continue
		}
		n := strings.ToUpper(strings.TrimSpace(p.Name))
		if n == CreatorName || strings.HasPrefix(n, CreatorName) || strings.Contains(p.Meta, "wdtt-wb1-creator") {
			return p
		}
		if p.spoke.Load() != 0 {
			spoke = p
		}
	}
	return spoke
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
