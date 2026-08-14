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
	MagicSize     = 4
	SIDSize       = 8
	NonceSize     = chacha20poly1305.NonceSize
	KeySize       = chacha20poly1305.KeySize
	MaxPayload    = 1024 // WrapVP8(Pack(max)) must fit one Pion VP8 RTP payload (<=1199)
	VP8MaxSample  = 1199 // Pion VP8Payloader outboundMTU=1200 minus 1-byte descriptor
	ARQWindow     = 1024
	initialFlight = 32   // flight/cwnd until a valid peer ACK (mixed old peers)
	StreamRecvCap = 256 * 1024 // per-stream app recv cap; Data is not ACKed until admitted here
	headerLen     = MagicSize + 1 + 2 + SIDSize + SIDSize
	relHdrLen     = 4 + 4 + 4 // epoch + seq + stream_id
	ackBodyLen    = 4 + 8 + 2 // cumAck + sack64 + recvWnd (minimum; extra SACK words may follow)
	sackWords     = 16        // 1024 SACK bits
	ackExtLen     = ackBodyLen + (sackWords-1)*8
)

// SessionID is a random 8-byte endpoint id for one RoomSession (not LiveKit identity).
type SessionID [SIDSize]byte

var (
	Magic   = [MagicSize]byte{'W', 'B', '1', 0x03}
	MagicV2 = [MagicSize]byte{'W', 'B', '1', 0x02}
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
	TypeAck   byte = 8
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
	errV1Frame       = errors.New("wb1: v1 frame rejected (need v3 panel+client)")
	errV2Frame       = errors.New("wb1: v2 frame rejected (need v3 panel+client)")
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
	Epoch    uint32
	Seq      uint32
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

func aadWire(typ byte, dest, src SessionID) []byte {
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

// PeekRoute reads cleartext v3 type/dest/src without decrypting.
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

// Pack seals a v3 frame. Dest/src are in the clear and in AEAD AAD.
// Plaintext is epoch || seq || stream_id || payload.
func Pack(key []byte, f Frame) ([]byte, error) {
	a, err := aead(key)
	if err != nil {
		return nil, err
	}
	return packWithAEAD(a, f)
}

func packWithAEAD(a cipher.AEAD, f Frame) ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, errPayloadTooBig
	}
	if f.Dest.IsZero() && !allowsZeroDest(f) {
		return nil, errZeroDest
	}
	if f.Src.IsZero() {
		return nil, errZeroSrc
	}
	plain := make([]byte, relHdrLen+len(f.Payload))
	binary.BigEndian.PutUint32(plain[0:4], f.Epoch)
	binary.BigEndian.PutUint32(plain[4:8], f.Seq)
	binary.BigEndian.PutUint32(plain[8:12], f.StreamID)
	copy(plain[relHdrLen:], f.Payload)

	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := a.Seal(nil, nonce, plain, aadWire(f.Type, f.Dest, f.Src))
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

// Unpack opens a sealed v3 frame. v1 and v2 magic are explicit errors.
func Unpack(key []byte, wire []byte) (Frame, error) {
	a, err := aead(key)
	if err != nil {
		return Frame{}, err
	}
	return unpackWithAEAD(a, wire)
}

func unpackWithAEAD(a cipher.AEAD, wire []byte) (Frame, error) {
	var z Frame
	if len(wire) < headerLen+NonceSize+16 {
		return z, errShortFrame
	}
	if bytesEqual(wire[:MagicSize], MagicV1[:]) {
		return z, errV1Frame
	}
	if bytesEqual(wire[:MagicSize], MagicV2[:]) {
		return z, errV2Frame
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
	plain, err := a.Open(nil, nonce, ct, aadWire(typ, z.Dest, z.Src))
	if err != nil {
		return z, err
	}
	if len(plain) < relHdrLen {
		return z, errShortFrame
	}
	z.Type = typ
	z.Epoch = binary.BigEndian.Uint32(plain[0:4])
	z.Seq = binary.BigEndian.Uint32(plain[4:8])
	z.StreamID = binary.BigEndian.Uint32(plain[8:12])
	if len(plain) > relHdrLen {
		z.Payload = append([]byte(nil), plain[relHdrLen:]...)
	}
	return z, nil
}

func isReliable(typ byte) bool {
	switch typ {
	case TypeHello, TypeOpen, TypeData, TypeFin, TypeErr:
		return true
	default:
		return false
	}
}

func packAckPayload(cum uint32, sack uint64, wnd uint16) []byte {
	var bits sackBitmap
	bits[0] = sack
	return packAckBitmap(cum, bits, wnd)
}

type sackBitmap [sackWords]uint64

func (s *sackBitmap) set(bit uint32) {
	if bit >= uint32(sackWords)*64 {
		return
	}
	s[bit/64] |= uint64(1) << (bit % 64)
}

func (s sackBitmap) has(bit uint32) bool {
	if bit >= uint32(sackWords)*64 {
		return false
	}
	return s[bit/64]&(uint64(1)<<(bit%64)) != 0
}

func packAckBitmap(cum uint32, sack sackBitmap, wnd uint16) []byte {
	p := make([]byte, ackExtLen)
	binary.BigEndian.PutUint32(p[0:4], cum)
	binary.BigEndian.PutUint64(p[4:12], sack[0])
	binary.BigEndian.PutUint16(p[12:14], wnd)
	for i := 1; i < sackWords; i++ {
		binary.BigEndian.PutUint64(p[14+(i-1)*8:], sack[i])
	}
	return p
}

func unpackAckPayload(p []byte) (cum uint32, sack uint64, wnd uint16, ok bool) {
	cum, bits, wnd, ok := unpackAckBitmap(p)
	if !ok {
		return 0, 0, 0, false
	}
	return cum, bits[0], wnd, true
}

func unpackAckBitmap(p []byte) (cum uint32, sack sackBitmap, wnd uint16, ok bool) {
	if len(p) < ackBodyLen {
		return 0, sack, 0, false
	}
	cum = binary.BigEndian.Uint32(p[0:4])
	sack[0] = binary.BigEndian.Uint64(p[4:12])
	wnd = binary.BigEndian.Uint16(p[12:14])
	extra := p[14:]
	for i := 1; i < sackWords && len(extra) >= 8; i++ {
		sack[i] = binary.BigEndian.Uint64(extra[:8])
		extra = extra[8:]
	}
	return cum, sack, wnd, true
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
