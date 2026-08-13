package wb1

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	MagicSize  = 4
	NonceSize  = chacha20poly1305.NonceSize
	KeySize    = chacha20poly1305.KeySize
	MaxPayload = 8192 // LiveKit data packets ~15KiB; 1KB chunks + blocking VP8 capped SOCKS at ~1 MB/s
	headerLen  = MagicSize + 1 + 2
)

var Magic = [MagicSize]byte{'W', 'B', '1', 0x01}

const (
	TypePing byte = 1
	TypePong byte = 2
	TypeOpen byte = 3
	TypeData byte = 4
	TypeFin  byte = 5
	TypeErr  byte = 6
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
	errBadLength     = errors.New("wb1: length mismatch")
	errPayloadTooBig = errors.New("wb1: payload too large")
)

// Frame is one WDTT-WB1 message after AEAD open.
type Frame struct {
	Type     byte
	StreamID uint32
	Payload  []byte
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

// Pack seals a frame. typ is also in the clear after magic.
func Pack(key []byte, f Frame) ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, errPayloadTooBig
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
	aad := append(Magic[:], f.Type)
	ct := a.Seal(nil, nonce, plain, aad)
	nct := len(nonce) + len(ct)
	if nct > 0xffff {
		return nil, fmt.Errorf("wb1: sealed frame too large")
	}
	out := make([]byte, headerLen+nct)
	copy(out[:MagicSize], Magic[:])
	out[MagicSize] = f.Type
	binary.BigEndian.PutUint16(out[MagicSize+1:MagicSize+3], uint16(nct))
	copy(out[headerLen:], nonce)
	copy(out[headerLen+NonceSize:], ct)
	return out, nil
}

// Unpack opens a sealed frame.
func Unpack(key []byte, wire []byte) (Frame, error) {
	var z Frame
	if len(wire) < headerLen+NonceSize+16 {
		return z, errShortFrame
	}
	if !bytesEqual(wire[:MagicSize], Magic[:]) {
		return z, errBadMagic
	}
	typ := wire[MagicSize]
	nct := int(binary.BigEndian.Uint16(wire[MagicSize+1 : MagicSize+3]))
	if headerLen+nct != len(wire) {
		return z, errBadLength
	}
	nonce := wire[headerLen : headerLen+NonceSize]
	ct := wire[headerLen+NonceSize:]
	a, err := aead(key)
	if err != nil {
		return z, err
	}
	aad := append(Magic[:], typ)
	plain, err := a.Open(nil, nonce, ct, aad)
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
