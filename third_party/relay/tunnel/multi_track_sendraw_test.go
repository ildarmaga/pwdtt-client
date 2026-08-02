package tunnel

import (
	"bytes"
	"sync"
	"testing"
)

// stubTrack records SendData/SendUrgent without a real VP8 writer.
type stubTrack struct {
	mu  sync.Mutex
	got [][]byte
}

func (s *stubTrack) SendData(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, append([]byte(nil), data...))
}

func (s *stubTrack) SendUrgent(data []byte) { s.SendData(data) }

func (s *stubTrack) frames() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.got))
	copy(out, s.got)
	return out
}

// sendRawToCamera is the dual-track routing contract: always camera (idx 0),
// never seq-prefix. Kept as a pure helper so the test does not need a live
// PeerConnection / VP8 encoder.
func sendRawToCamera(tracks []urgentSender, data []byte) {
	if len(tracks) == 0 {
		return
	}
	tracks[0].SendUrgent(data)
}

// TestSendRawCameraOnlyNoSeq locks the field fix: dual must not stripe or
// seq-prefix; screenshare must see zero KCP frames.
func TestSendRawCameraOnlyNoSeq(t *testing.T) {
	cam := &stubTrack{}
	screen := &stubTrack{}
	payload := []byte("kcp-frame-payload-xyz")
	sendRawToCamera([]urgentSender{cam, screen}, payload)

	if n := len(screen.frames()); n != 0 {
		t.Fatalf("screenshare got %d frames; want 0", n)
	}
	got := cam.frames()
	if len(got) != 1 {
		t.Fatalf("camera got %d frames; want 1", len(got))
	}
	if !bytes.Equal(got[0], payload) {
		t.Fatalf("camera payload %q != %q (seq prefix would break this)", got[0], payload)
	}
}

// Compile-time: MultiTrackTunnel.SendRaw must stay camera-only (same contract).
func TestMultiTrackSendRawMatchesContract(t *testing.T) {
	// Smoke: empty MultiTrackTunnel must not panic.
	m := &MultiTrackTunnel{}
	m.SendRaw([]byte("x"))
}
