package wb1

import (
	"bytes"
	"crypto/sha256"
)

// IdentityHash is a stable 8-byte routing id for a LiveKit identity.
func IdentityHash(identity string) [8]byte {
	sum := sha256.Sum256([]byte(identity))
	var h [8]byte
	copy(h[:], sum[:8])
	return h
}

// WrapVP8 appends dest hash + WDTT-WB1 frame after a real 16×16 VP8 keyframe
// so the SFU still sees a valid video payload (extra bytes ride along).
func WrapVP8(dest [8]byte, frame []byte) []byte {
	out := make([]byte, len(vp8Keyframe)+8+len(frame))
	copy(out, vp8Keyframe)
	copy(out[len(vp8Keyframe):], dest[:])
	copy(out[len(vp8Keyframe)+8:], frame)
	return out
}

// UnwrapVP8 finds a WDTT-WB1 frame inside an RTP/VP8 payload.
func UnwrapVP8(payload []byte) (dest [8]byte, frame []byte, ok bool) {
	idx := bytes.Index(payload, Magic[:])
	if idx < 0 {
		return dest, nil, false
	}
	if idx >= 8 {
		copy(dest[:], payload[idx-8:idx])
	}
	return dest, payload[idx:], true
}

// DestForMe reports whether a VP8 dest stamp is for this identity.
// Zero dest means "this track is already unicast".
func DestForMe(me, dest [8]byte) bool {
	var zero [8]byte
	if dest == zero {
		return true
	}
	return dest == me
}
