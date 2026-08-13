package wb1

import (
	"bytes"
	"testing"
)

func TestWrapUnwrapVP8(t *testing.T) {
	key, err := DeriveKey("secret", "room")
	if err != nil {
		t.Fatal(err)
	}
	frame, err := Pack(key, Frame{Type: TypePing, StreamID: 1, Payload: []byte("hi")})
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

func TestUnwrapVP8FindsMagicInKeyframe(t *testing.T) {
	key, _ := DeriveKey("secret", "room")
	frame, _ := Pack(key, Frame{Type: TypeData, StreamID: 2, Payload: []byte("x")})
	mixed := append(append([]byte{}, vp8Keyframe...), WrapVP8(IdentityHash("b"), frame)...)
	_, got, ok := UnwrapVP8(mixed)
	if !ok || !bytes.Equal(got, frame) {
		t.Fatal("did not find framed payload")
	}
}

func TestDestFilter(t *testing.T) {
	mine := IdentityHash("me")
	other := IdentityHash("other")
	if !DestForMe(mine, [8]byte{}) {
		t.Fatal("zero dest is for everyone on this track")
	}
	if !DestForMe(mine, mine) {
		t.Fatal("own dest should match")
	}
	if DestForMe(mine, other) {
		t.Fatal("other dest must be skipped")
	}
}
