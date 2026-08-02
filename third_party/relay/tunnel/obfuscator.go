package tunnel

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// peerHandoverSilence is how long the current peer must be silent before a
// frame from a different (non-stale) epoch is accepted as a real reconnect.
// While the current peer is still live, a competing publisher in the same
// room (e.g. a second device sharing the room) is ignored instead of forcing
// a KCP+smux reset — which otherwise cascades into a peer-restart storm.
const peerHandoverSilence = 2 * time.Second

var vp8Keepalive = []byte{
	0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0x10, 0x00,
	0x10, 0x00, 0x00, 0x47, 0x08, 0x85, 0x85, 0x88,
	0x99, 0x84, 0x88, 0xfc,
}

var vp8Interframe = []byte{
	0xb1, 0x01, 0x00, 0x08, 0x11, 0x18, 0x00, 0x18,
	0x00, 0x18, 0x58, 0x2f, 0xf4, 0x00, 0x08, 0x00,
	0x00,
}

const (
	vp8KeepaliveLen   = 20
	vp8InterframeLen  = 17
	epochFieldLen     = 4
	keepaliveHdrLen   = vp8KeepaliveLen + epochFieldLen
	interframeHdrLen  = vp8InterframeLen + epochFieldLen
)

var ErrEmptySecret = errors.New("tunnel: obfuscator requires a non-empty secret")

type DecodeResult struct {
	HasFrame    bool
	Keepalive   bool
	SelfEcho    bool
	PeerRestart bool
	Payload     []byte
	PeerEpoch   uint32
}

type TunnelObfuscator struct {
	aead       cipher.AEAD
	localEpoch uint32

	mu             sync.Mutex
	peerEpoch      uint32
	hasPeer        bool
	lastPeerActive time.Time           // last time a frame from peerEpoch was accepted
	staleEpochs    map[uint32]struct{} // superseded peer epochs (stale VP8 from old carrier)

	nowFn func() time.Time // injectable clock for tests; nil → time.Now
}

func (o *TunnelObfuscator) now() time.Time {
	if o.nowFn != nil {
		return o.nowFn()
	}
	return time.Now()
}

func DeriveSecretFromJoinLink(joinLink string) []byte {
	token := extractJoinToken(joinLink)
	if token == "" {
		return nil
	}
	return []byte(token)
}

func extractJoinToken(joinLink string) string {
	s := strings.TrimSpace(joinLink)
	s = strings.TrimRight(s, "/")
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func NewTunnelObfuscator(secret []byte) (*TunnelObfuscator, error) {
	if len(secret) == 0 {
		return nil, ErrEmptySecret
	}
	keyHash := sha256.Sum256(secret)
	aead, err := chacha20poly1305.NewX(keyHash[:])
	if err != nil {
		return nil, err
	}
	var epochBytes [4]byte
	if _, err := rand.Read(epochBytes[:]); err != nil {
		return nil, err
	}
	epoch := binary.BigEndian.Uint32(epochBytes[:])
	if epoch == 0 {
		epoch = 1
	}
	return &TunnelObfuscator{aead: aead, localEpoch: epoch}, nil
}

func (o *TunnelObfuscator) LocalEpoch() uint32 { return o.localEpoch }

// ResetPeer clears sticky peer-epoch state so a new joiner after SFU recycle
// is accepted immediately (otherwise peerHandoverSilence drops MsgConfig /
// MsgConnect while "resending vp8 config" forever).
func (o *TunnelObfuscator) ResetPeer() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.hasPeer = false
	o.peerEpoch = 0
	o.lastPeerActive = time.Time{}
	o.staleEpochs = nil
}

func (o *TunnelObfuscator) keepaliveHeader() []byte {
	hdr := make([]byte, keepaliveHdrLen)
	copy(hdr, vp8Keepalive)
	binary.BigEndian.PutUint32(hdr[vp8KeepaliveLen:], o.localEpoch)
	return hdr
}

func (o *TunnelObfuscator) dataHeader() []byte {
	hdr := make([]byte, interframeHdrLen)
	copy(hdr, vp8Interframe)
	binary.BigEndian.PutUint32(hdr[vp8InterframeLen:], o.localEpoch)
	return hdr
}

func (o *TunnelObfuscator) EncodeKeepalive() []byte {
	return o.keepaliveHeader()
}

func (o *TunnelObfuscator) EncodeData(payload []byte) []byte {
	hdr := o.dataHeader()
	nonce := make([]byte, o.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil
	}
	out := make([]byte, 0, len(hdr)+len(nonce)+len(payload)+o.aead.Overhead())
	out = append(out, hdr...)
	out = append(out, nonce...)
	out = o.aead.Seal(out, nonce, payload, nil)
	return out
}

func (o *TunnelObfuscator) EncryptPayload(plaintext []byte) []byte {
	if o == nil {
		return plaintext
	}
	nonce := make([]byte, o.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+o.aead.Overhead())
	out = append(out, nonce...)
	return o.aead.Seal(out, nonce, plaintext, nil)
}

func (o *TunnelObfuscator) DecryptPayload(data []byte) ([]byte, bool) {
	if o == nil {
		return data, true
	}
	nonceSize := o.aead.NonceSize()
	if len(data) < nonceSize+o.aead.Overhead() {
		return nil, false
	}
	nonce := data[:nonceSize]
	ciphertext := data[nonceSize:]
	plaintext, err := o.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, false
	}
	return plaintext, true
}

func (o *TunnelObfuscator) Decode(frame []byte) DecodeResult {
	if len(frame) < 1 {
		return DecodeResult{}
	}
	var hdrLen, epochOff int
	switch frame[0] {
	case vp8Keepalive[0]:
		hdrLen = keepaliveHdrLen
		epochOff = vp8KeepaliveLen
	case vp8Interframe[0]:
		hdrLen = interframeHdrLen
		epochOff = vp8InterframeLen
	default:
		return DecodeResult{}
	}
	if len(frame) < hdrLen {
		return DecodeResult{}
	}
	peerEpoch := binary.BigEndian.Uint32(frame[epochOff : epochOff+epochFieldLen])
	if peerEpoch == o.localEpoch {
		return DecodeResult{HasFrame: true, SelfEcho: true, PeerEpoch: peerEpoch}
	}

	res := DecodeResult{HasFrame: true, PeerEpoch: peerEpoch}
	o.mu.Lock()
	switch {
	case !o.hasPeer:
		o.peerEpoch = peerEpoch
		o.hasPeer = true
		o.lastPeerActive = o.now()
	case o.peerEpoch == peerEpoch:
		o.lastPeerActive = o.now()
	default:
		if o.isStaleEpochLocked(peerEpoch) {
			o.mu.Unlock()
			if len(frame) == hdrLen {
				return DecodeResult{HasFrame: true, Keepalive: true, PeerEpoch: peerEpoch}
			}
			return DecodeResult{}
		}
		// Sticky peer: while the current peer is still live, a frame from a
		// different epoch is a competing publisher (e.g. a second device in
		// the same room), not a reconnect. Ignore it instead of resetting
		// KCP — otherwise two coexisting publishers ping-pong the epoch and
		// storm peer-restarts. Only hand over after real silence.
		if o.now().Sub(o.lastPeerActive) < peerHandoverSilence {
			o.mu.Unlock()
			return DecodeResult{}
		}
		o.markStaleEpochLocked(o.peerEpoch)
		o.peerEpoch = peerEpoch
		o.lastPeerActive = o.now()
		res.PeerRestart = true
	}
	o.mu.Unlock()

	if len(frame) == hdrLen {
		res.Keepalive = true
		return res
	}

	body := frame[hdrLen:]
	nonceSize := o.aead.NonceSize()
	if len(body) < nonceSize+o.aead.Overhead() {
		return DecodeResult{}
	}
	nonce := body[:nonceSize]
	ciphertext := body[nonceSize:]
	plaintext, err := o.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return DecodeResult{}
	}
	res.Payload = plaintext
	return res
}

func (o *TunnelObfuscator) isStaleEpochLocked(epoch uint32) bool {
	if o.staleEpochs == nil {
		return false
	}
	_, ok := o.staleEpochs[epoch]
	return ok
}

func (o *TunnelObfuscator) markStaleEpochLocked(epoch uint32) {
	if epoch == 0 {
		return
	}
	if o.staleEpochs == nil {
		o.staleEpochs = make(map[uint32]struct{}, 4)
	}
	o.staleEpochs[epoch] = struct{}{}
	if len(o.staleEpochs) <= 8 {
		return
	}
	for e := range o.staleEpochs {
		if e != o.peerEpoch {
			delete(o.staleEpochs, e)
		}
	}
}
