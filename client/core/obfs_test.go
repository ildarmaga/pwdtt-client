package core

import (
	"bytes"
	"testing"
)

func TestObfsAudioAndVideoRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, wrapKeyLen)
	payload := []byte("hello-dtls-payload-0123456789")

	for _, mode := range []string{"audio", "video", ""} {
		cfg := NewObfsConfig(mode)
		st := NewObfsState()
		wantPT := uint8(111)
		wantPad := 24
		if mode == "video" {
			wantPT = 96
			wantPad = 60
		}
		if cfg.PayloadType != wantPT {
			t.Fatalf("mode=%q: PT=%d want %d", mode, cfg.PayloadType, wantPT)
		}
		if cfg.PaddingMax != wantPad {
			t.Fatalf("mode=%q: PaddingMax=%d want %d", mode, cfg.PaddingMax, wantPad)
		}

		wire, err := obfsWrapPacket(key, payload, cfg, st)
		if err != nil {
			t.Fatalf("mode=%q wrap: %v", mode, err)
		}
		if !obfsIsRTPPacket(wire) {
			t.Fatalf("mode=%q: obfsIsRTPPacket=false PT=%d", mode, wire[1]&0x7F)
		}
		if wire[1]&0x7F != wantPT {
			t.Fatalf("mode=%q: wire PT=%d want %d", mode, wire[1]&0x7F, wantPT)
		}

		dst := make([]byte, len(payload)+8)
		n, err := obfsUnwrapPacket(key, wire, dst)
		if err != nil {
			t.Fatalf("mode=%q unwrap: %v", mode, err)
		}
		if !bytes.Equal(dst[:n], payload) {
			t.Fatalf("mode=%q: payload mismatch", mode)
		}
	}
}

func TestObfsIsRTPPacketRejectsOtherPT(t *testing.T) {
	wire := make([]byte, 40)
	wire[0] = 0x80
	wire[1] = 100 // not 111/96
	if obfsIsRTPPacket(wire) {
		t.Fatal("unexpected accept for PT 100")
	}
}
