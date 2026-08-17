package wb1

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
)

// IdentityHash is a stable 8-byte id derived from a LiveKit identity.
// Production routing uses SessionID, not this hash.
func IdentityHash(identity string) SessionID {
	sum := sha256.Sum256([]byte(identity))
	var h SessionID
	copy(h[:], sum[:8])
	return h
}

// WrapVP8 appends dest SID + WDTT-WB1 frame after a real 16×16 VP8 keyframe
// so the SFU still sees a valid video payload (extra bytes ride along).
func WrapVP8(dest SessionID, frame []byte) []byte {
	out := make([]byte, len(vp8Keyframe)+SIDSize+len(frame))
	copy(out, vp8Keyframe)
	copy(out[len(vp8Keyframe):], dest[:])
	copy(out[len(vp8Keyframe)+SIDSize:], frame)
	return out
}

// UnwrapVP8 finds a WDTT-WB1 v3 frame inside an RTP/VP8 payload.
func UnwrapVP8(payload []byte) (dest SessionID, frame []byte, ok bool) {
	idx := bytes.Index(payload, Magic[:])
	if idx < SIDSize || idx+headerLen > len(payload) {
		return dest, nil, false
	}
	nct := int(binary.BigEndian.Uint16(payload[idx+MagicSize+1 : idx+MagicSize+3]))
	end := idx + headerLen + nct
	if nct < NonceSize+16 || end > len(payload) {
		return dest, nil, false
	}
	copy(dest[:], payload[idx-SIDSize:idx])
	return dest, payload[idx:end], true
}

// DestForMe reports whether a VP8 dest stamp is for this endpoint.
// Zero dest means broadcast (creator beacon).
func DestForMe(me, dest SessionID) bool {
	if dest.IsZero() {
		return true
	}
	return dest == me
}

// publishPlan is the production send policy: mux payloads go VP8 only;
// creator beacon (zero dest) also rides the LiveKit data topic for discovery.
func isControlType(typ byte) bool {
	switch typ {
	case TypePing, TypePong, TypeAck:
		return true
	default:
		return false
	}
}

func publishPlan(dest SessionID, typ byte) (dataTopic, vp8 bool) {
	if dest.IsZero() {
		return true, true
	}
	// Ping/Pong/ACK must not wait behind bulk VP8 WriteSample (speedtest stalls WBT).
	if isControlType(typ) {
		return true, false
	}
	return false, true
}
