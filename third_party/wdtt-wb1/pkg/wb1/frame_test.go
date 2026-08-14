package wb1

import (
	"bytes"
	"testing"
)

func TestDeriveKeyDeterministic(t *testing.T) {
	a, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same password+room must yield the same key")
	}
	if len(a) != KeySize {
		t.Fatalf("key len %d, want %d", len(a), KeySize)
	}
}

func TestDeriveKeyDiffersByPasswordAndRoom(t *testing.T) {
	base, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	otherPass, err := DeriveKey("other", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	otherRoom, err := DeriveKey("secret", "room-2")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(base, otherPass) {
		t.Fatal("different password must change the key")
	}
	if bytes.Equal(base, otherRoom) {
		t.Fatal("different room must change the key")
	}
}

func TestDeriveKeyRejectsEmpty(t *testing.T) {
	if _, err := DeriveKey("", "room"); err == nil {
		t.Fatal("empty password must fail")
	}
	if _, err := DeriveKey("secret", ""); err == nil {
		t.Fatal("empty room must fail")
	}
}

func TestPackUnpackRoundTrip(t *testing.T) {
	key, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	plain := Frame{
		Type:     TypeData,
		StreamID: 42,
		Dest:     testSID(1),
		Src:      testSID(2),
		Payload:  []byte("hello wb1"),
	}
	wire, err := Pack(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) < MagicSize+1+2+NonceSize+16 {
		t.Fatalf("wire too short: %d", len(wire))
	}
	if !bytes.Equal(wire[:MagicSize], Magic[:]) {
		t.Fatalf("bad magic: %x", wire[:MagicSize])
	}
	if wire[MagicSize] != TypeData {
		t.Fatalf("cleartext type %d, want %d", wire[MagicSize], TypeData)
	}
	got, err := Unpack(key, wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != plain.Type || got.StreamID != plain.StreamID {
		t.Fatalf("header mismatch: %+v vs %+v", got, plain)
	}
	if !bytes.Equal(got.Payload, plain.Payload) {
		t.Fatalf("payload %q vs %q", got.Payload, plain.Payload)
	}
}

func TestUnpackRejectsWrongKey(t *testing.T) {
	key, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	other, err := DeriveKey("other", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Pack(key, Frame{Type: TypePing, StreamID: 0, Dest: testSID(1), Src: testSID(2), Payload: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unpack(other, wire); err == nil {
		t.Fatal("wrong key must fail AEAD open")
	}
}

func TestUnpackRejectsTamper(t *testing.T) {
	key, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: testSID(1), Src: testSID(2), Payload: []byte("abc")})
	if err != nil {
		t.Fatal(err)
	}
	wire[len(wire)-1] ^= 0xff
	if _, err := Unpack(key, wire); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}

func TestUnpackRejectsBadMagic(t *testing.T) {
	key, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Pack(key, Frame{Type: TypePing, Dest: testSID(1), Src: testSID(2)})
	if err != nil {
		t.Fatal(err)
	}
	wire[0] ^= 0xff
	if _, err := Unpack(key, wire); err == nil {
		t.Fatal("bad magic must fail")
	}
}

func TestPackEmptyPayload(t *testing.T) {
	key, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Pack(key, Frame{Type: TypeFin, StreamID: 7, Dest: testSID(1), Src: testSID(2)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unpack(key, wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeFin || got.StreamID != 7 || len(got.Payload) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestPackMaxPayloadRoundTrip(t *testing.T) {
	if MaxPayload < 8000 {
		t.Fatalf("MaxPayload=%d; SOCKS over WB needs >=8000 or each Send is ~1KB and VP8 pacing caps ~1 MB/s", MaxPayload)
	}
	key, err := DeriveKey("secret", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("w"), MaxPayload)
	wire, err := Pack(key, Frame{Type: TypeData, StreamID: 9, Dest: testSID(1), Src: testSID(2), Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unpack(key, wire)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("roundtrip len %d want %d", len(got.Payload), len(payload))
	}
}
