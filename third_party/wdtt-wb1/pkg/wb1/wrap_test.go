package wb1

import (
	"bytes"
	"testing"

	"github.com/pion/rtp/codecs"
)

func TestWrapUnwrapVP8(t *testing.T) {
	key, err := DeriveKey("secret", "room")
	if err != nil {
		t.Fatal(err)
	}
	frame, err := Pack(key, Frame{Type: TypePing, StreamID: 1, Dest: IdentityHash("joiner-a"), Src: testSID(1), Payload: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	dest := IdentityHash("joiner-a")
	wire := WrapVP8(dest, frame)
	if !bytes.HasPrefix(wire, vp8Keyframe) {
		t.Fatal("wrap must start with a real VP8 keyframe")
	}
	gotDest, gotFrame, ok := UnwrapVP8(wire)
	if !ok {
		t.Fatal("unwrap failed")
	}
	if gotDest != dest {
		t.Fatalf("dest %x vs %x", gotDest, dest)
	}
	if !bytes.Equal(gotFrame, frame) {
		t.Fatal("frame mismatch")
	}
}

func TestUnwrapVP8TrimsRTPPadding(t *testing.T) {
	key, err := DeriveKey("secret", "room")
	if err != nil {
		t.Fatal(err)
	}
	dest := testSID(1)
	frame, err := Pack(key, Frame{
		Type: TypeData, StreamID: 7, Dest: dest, Src: testSID(2), Payload: []byte("payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	wire := append(WrapVP8(dest, frame), 0xaa, 0xbb, 0xcc)
	_, gotFrame, ok := UnwrapVP8(wire)
	if !ok {
		t.Fatal("unwrap failed")
	}
	if !bytes.Equal(gotFrame, frame) {
		t.Fatalf("frame includes RTP padding: got %d bytes, want %d", len(gotFrame), len(frame))
	}
	if _, err := Unpack(key, gotFrame); err != nil {
		t.Fatalf("trimmed frame must decrypt: %v", err)
	}
}

func TestUnwrapVP8FindsMagicInKeyframe(t *testing.T) {
	key, _ := DeriveKey("secret", "room")
	frame, _ := Pack(key, Frame{Type: TypeData, StreamID: 2, Dest: IdentityHash("b"), Src: testSID(1), Payload: []byte("x")})
	mixed := append(append([]byte{}, vp8Keyframe...), WrapVP8(IdentityHash("b"), frame)...)
	_, got, ok := UnwrapVP8(mixed)
	if !ok || !bytes.Equal(got, frame) {
		t.Fatal("did not find framed payload")
	}
}

func TestMaxPayloadWrapFitsOnePionVP8RTP(t *testing.T) {
	key, err := DeriveKey("secret", "room")
	if err != nil {
		t.Fatal(err)
	}
	dest, src := testSID(1), testSID(2)
	payload := bytes.Repeat([]byte("x"), MaxPayload)
	frame, err := Pack(key, Frame{
		Type: TypeData, StreamID: 9, Dest: dest, Src: src,
		Epoch: 1, Seq: 1, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := WrapVP8(dest, frame)
	if len(wrapped) > VP8MaxSample {
		t.Fatalf("WrapVP8(Pack(MaxPayload))=%d bytes, want <=%d", len(wrapped), VP8MaxSample)
	}
	payloader := codecs.VP8Payloader{}
	pkts := payloader.Payload(1200, wrapped)
	if len(pkts) != 1 {
		t.Fatalf("Pion VP8 MTU 1200 fragmented MaxPayload wrap into %d packets (wrap=%d)", len(pkts), len(wrapped))
	}
	_, got, ok := UnwrapVP8(wrapped)
	if !ok {
		t.Fatal("unwrap after single RTP failed")
	}
	if _, err := Unpack(key, got); err != nil {
		t.Fatalf("unpack: %v", err)
	}
}

func TestUnwrapVP8RejectsTruncatedMaxPayload(t *testing.T) {
	key, err := DeriveKey("secret", "room")
	if err != nil {
		t.Fatal(err)
	}
	dest := testSID(3)
	frame, err := Pack(key, Frame{
		Type: TypeData, StreamID: 1, Dest: dest, Src: testSID(4),
		Payload: bytes.Repeat([]byte("y"), MaxPayload),
	})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := WrapVP8(dest, frame)
	if _, _, ok := UnwrapVP8(wrapped[:len(wrapped)-1]); ok {
		t.Fatal("truncated wrap must not parse")
	}
	if _, _, ok := UnwrapVP8(wrapped[:len(vp8Keyframe)+SIDSize+headerLen]); ok {
		t.Fatal("header-only RTP must not parse a partial frame")
	}
}

func TestDestFilter(t *testing.T) {
	mine := IdentityHash("me")
	other := IdentityHash("other")
	if !DestForMe(mine, SessionID{}) {
		t.Fatal("zero dest is for everyone on this track")
	}
	if !DestForMe(mine, mine) {
		t.Fatal("own dest should match")
	}
	if DestForMe(mine, other) {
		t.Fatal("other dest must be skipped")
	}
}
