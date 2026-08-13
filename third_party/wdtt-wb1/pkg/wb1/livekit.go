package wb1

import (
	"context"
	"sync"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const DataTopic = "wdtt-wb1"

// 16×16 VP8 keyframe — satisfies WB's "publisher must send video" constraint.
var vp8Keyframe = []byte{
	0x90, 0x00, 0x00, 0x9d, 0x01, 0x2a, 0x10, 0x00, 0x10, 0x00,
	0x02, 0x47, 0x08, 0x85, 0x85, 0x88, 0x85, 0x84, 0x88, 0x02,
	0x02, 0x00, 0x0c, 0x0d, 0x60, 0x00, 0xfe, 0xfc, 0x5a, 0x00, 0x00,
}

// RoomSession is a LiveKit room used as a WDTT-WB1 packet carrier.
type RoomSession struct {
	room   *lksdk.Room
	recv   chan []byte
	done   chan struct{}
	cancel context.CancelFunc

	mu     sync.Mutex
	labels []string
}

// ConnectRoom joins LiveKit with a WB-issued room JWT, publishes dummy VP8,
// and receives reliable data on topic wdtt-wb1.
func ConnectRoom(parent context.Context, serverURL, roomToken string) (*RoomSession, error) {
	ctx, cancel := context.WithCancel(parent)
	s := &RoomSession{
		recv:   make(chan []byte, 64),
		done:   make(chan struct{}),
		cancel: cancel,
	}
	cb := &lksdk.RoomCallback{
		OnDisconnected: func() {
			s.refreshLabels()
			s.cancel()
		},
		OnDisconnectedWithReason: func(_ lksdk.DisconnectionReason) {
			s.refreshLabels()
			s.cancel()
		},
		OnParticipantConnected: func(_ *lksdk.RemoteParticipant) {
			s.refreshLabels()
		},
		OnParticipantDisconnected: func(_ *lksdk.RemoteParticipant) {
			s.refreshLabels()
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
				s.push(ud.Payload)
			},
		},
	}
	room, err := lksdk.ConnectToRoomWithToken(serverURL, roomToken, cb, lksdk.WithAutoSubscribe(true))
	if err != nil {
		cancel()
		return nil, err
	}
	s.room = room
	s.refreshLabels()
	if err := s.publishDummyVideo(ctx); err != nil {
		room.Disconnect()
		cancel()
		return nil, err
	}
	go func() {
		<-ctx.Done()
		room.Disconnect()
		close(s.done)
	}()
	return s, nil
}

func (s *RoomSession) push(p []byte) {
	if len(p) == 0 {
		return
	}
	cp := append([]byte(nil), p...)
	select {
	case s.recv <- cp:
	default:
	}
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
		VideoWidth:  16,
		VideoHeight: 16,
	}); err != nil {
		return err
	}
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_ = track.WriteSample(media.Sample{Data: vp8Keyframe, Duration: time.Second}, nil)
			}
		}
	}()
	return nil
}

// Send implements Carrier.
func (s *RoomSession) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pkt := lksdk.UserData(payload)
	pkt.Topic = DataTopic
	return s.room.LocalParticipant.PublishDataPacket(pkt, lksdk.WithDataPublishReliable(true), lksdk.WithDataPublishTopic(DataTopic))
}

// Recv implements Carrier.
func (s *RoomSession) Recv(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, context.Canceled
	case p := <-s.recv:
		return p, nil
	}
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

func (s *RoomSession) refreshLabels() {
	if s.room == nil {
		return
	}
	parts := s.room.GetRemoteParticipants()
	labels := make([]string, 0, len(parts)*2)
	for _, p := range parts {
		if p == nil {
			continue
		}
		if id := p.Identity(); id != "" {
			labels = append(labels, id)
		}
		if n := p.Name(); n != "" {
			labels = append(labels, n)
		}
	}
	s.mu.Lock()
	s.labels = labels
	s.mu.Unlock()
}

// PeerLabels returns LiveKit identity/name of remote participants.
func (s *RoomSession) PeerLabels() []string {
	s.refreshLabels()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.labels))
	copy(out, s.labels)
	return out
}
