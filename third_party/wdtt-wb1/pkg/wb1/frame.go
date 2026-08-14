package wb1

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	MagicSize  = 4
	SIDSize    = 8
	NonceSize  = chacha20poly1305.NonceSize
	KeySize    = chacha20poly1305.KeySize
	MaxPayload = 8192 // LiveKit data packets ~15KiB; 1KB chunks + blocking VP8 capped SOCKS at ~1 MB/s
	headerLen  = MagicSize + 1 + 2 + SIDSize + SIDSize
)

// SessionID is a random 8-byte endpoint id for one RoomSession (not LiveKit identity).
type SessionID [SIDSize]byte

var (
	Magic   = [MagicSize]byte{'W', 'B', '1', 0x02}
	MagicV1 = [MagicSize]byte{'W', 'B', '1', 0x01}
)

const (
	TypePing  byte = 1
	TypePong  byte = 2
	TypeOpen  byte = 3
	TypeData  byte = 4
	TypeFin   byte = 5
	TypeErr   byte = 6
	TypeHello byte = 7
)

const (
	hkdfSalt = "WDTT-WB1"
	hkdfInfo = "wb-aead/v1"
)

var (
	errEmptyPassword = errors.New("wb1: empty password")
	errEmptyRoom     = errors.New("wb1: empty room id")
	errShortFrame    = errors.New("wb1: frame too short")
	errBadMagic      = errors.New("wb1: bad magic")
	errV1Frame       = errors.New("wb1: v1 frame rejected (need v2 panel+client)")
	errBadLength     = errors.New("wb1: length mismatch")
	errPayloadTooBig = errors.New("wb1: payload too large")
	errZeroDest      = errors.New("wb1: zero dest")
	errZeroSrc       = errors.New("wb1: zero src")
	errBadSID        = errors.New("wb1: bad session id")
)

// Frame is one WDTT-WB1 message after AEAD open.
type Frame struct {
	Type     byte
	StreamID uint32
	Dest     SessionID
	Src      SessionID
	Payload  []byte
}

func (id SessionID) IsZero() bool {
	var z SessionID
	return id == z
}

func (id SessionID) Hex() string {
	return hex.EncodeToString(id[:])
}

// NewSessionID returns a random 8-byte endpoint id.
func NewSessionID() (SessionID, error) {
	var id SessionID
	if _, err := rand.Read(id[:]); err != nil {
		return id, err
	}
	if id.IsZero() {
		id[0] = 1
	}
	return id, nil
}

// ParseSessionIDHex decodes a 16-char hex session id.
func ParseSessionIDHex(s string) (SessionID, error) {
	var id SessionID
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != SIDSize {
		return id, errBadSID
	}
	copy(id[:], b)
	return id, nil
}

// DeriveKey returns a 32-byte ChaCha20-Poly1305 key from password + room_id.
func DeriveKey(password, roomID string) ([]byte, error) {
	if password == "" {
		return nil, errEmptyPassword
	}
	if roomID == "" {
		return nil, errEmptyRoom
	}
	ikm := make([]byte, 0, len(password)+1+len(roomID))
	ikm = append(ikm, password...)
	ikm = append(ikm, 0x00)
	ikm = append(ikm, roomID...)
	key := make([]byte, KeySize)
	r := hkdf.New(sha256.New, ikm, []byte(hkdfSalt), []byte(hkdfInfo))
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

func aead(key []byte) (cipher.AEAD, error) {
	return chacha20poly1305.New(key)
}

func aadV2(typ byte, dest, src SessionID) []byte {
	aad := make([]byte, MagicSize+1+SIDSize+SIDSize)
	copy(aad[:MagicSize], Magic[:])
	aad[MagicSize] = typ
	copy(aad[MagicSize+1:], dest[:])
	copy(aad[MagicSize+1+SIDSize:], src[:])
	return aad
}

func allowsZeroDest(f Frame) bool {
	return f.Type == TypePing && isBeaconPlain(f.Payload)
}

func isBeaconPlain(p []byte) bool {
	s := string(p)
	return s == CreatorBeaconPayload || strings.HasPrefix(s, CreatorBeaconPayload+"|")
}

// PeekRoute reads cleartext v2 type/dest/src without decrypting.
func PeekRoute(wire []byte) (typ byte, dest, src SessionID, ok bool) {
	if len(wire) < headerLen {
		return 0, dest, src, false
	}
	if !bytesEqual(wire[:MagicSize], Magic[:]) {
		return 0, dest, src, false
	}
	typ = wire[MagicSize]
	copy(dest[:], wire[MagicSize+1+2:MagicSize+1+2+SIDSize])
	copy(src[:], wire[MagicSize+1+2+SIDSize:headerLen])
	return typ, dest, src, true
}

// Pack seals a v2 frame. Dest/src are in the clear and in AEAD AAD.
func Pack(key []byte, f Frame) ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, errPayloadTooBig
	}
	if f.Dest.IsZero() && !allowsZeroDest(f) {
		return nil, errZeroDest
	}
	if f.Src.IsZero() {
		return nil, errZeroSrc
	}
	a, err := aead(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, 4+len(f.Payload))
	binary.BigEndian.PutUint32(plain[:4], f.StreamID)
	copy(plain[4:], f.Payload)

	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := a.Seal(nil, nonce, plain, aadV2(f.Type, f.Dest, f.Src))
	nct := len(nonce) + len(ct)
	if nct > 0xffff {
		return nil, fmt.Errorf("wb1: sealed frame too large")
	}
	out := make([]byte, headerLen+nct)
	copy(out[:MagicSize], Magic[:])
	out[MagicSize] = f.Type
	binary.BigEndian.PutUint16(out[MagicSize+1:MagicSize+3], uint16(nct))
	copy(out[MagicSize+3:], f.Dest[:])
	copy(out[MagicSize+3+SIDSize:], f.Src[:])
	copy(out[headerLen:], nonce)
	copy(out[headerLen+NonceSize:], ct)
	return out, nil
}

// Unpack opens a sealed v2 frame. v1 magic is an explicit error.
func Unpack(key []byte, wire []byte) (Frame, error) {
	var z Frame
	if len(wire) < headerLen+NonceSize+16 {
		return z, errShortFrame
	}
	if bytesEqual(wire[:MagicSize], MagicV1[:]) {
		return z, errV1Frame
	}
	if !bytesEqual(wire[:MagicSize], Magic[:]) {
		return z, errBadMagic
	}
	typ := wire[MagicSize]
	nct := int(binary.BigEndian.Uint16(wire[MagicSize+1 : MagicSize+3]))
	if headerLen+nct != len(wire) {
		return z, errBadLength
	}
	copy(z.Dest[:], wire[MagicSize+3:MagicSize+3+SIDSize])
	copy(z.Src[:], wire[MagicSize+3+SIDSize:headerLen])
	if z.Src.IsZero() {
		return Frame{}, errZeroSrc
	}
	nonce := wire[headerLen : headerLen+NonceSize]
	ct := wire[headerLen+NonceSize:]
	a, err := aead(key)
	if err != nil {
		return z, err
	}
	plain, err := a.Open(nil, nonce, ct, aadV2(typ, z.Dest, z.Src))
	if err != nil {
		return z, err
	}
	if len(plain) < 4 {
		return z, errShortFrame
	}
	z.Type = typ
	z.StreamID = binary.BigEndian.Uint32(plain[:4])
	if len(plain) > 4 {
		z.Payload = append([]byte(nil), plain[4:]...)
	}
	return z, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
