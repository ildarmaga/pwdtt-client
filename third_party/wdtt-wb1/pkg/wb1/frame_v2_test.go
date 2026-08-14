package wb1

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key, err := DeriveKey("secret", "room-v2")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testSID(b byte) SessionID {
	var id SessionID
	id[0] = b
	return id
}

func TestV3Magic(t *testing.T) {
	if Magic != [MagicSize]byte{'W', 'B', '1', 0x03} {
		t.Fatalf("Magic=%x, want WB1\\x03", Magic)
	}
	if MagicV2 != [MagicSize]byte{'W', 'B', '1', 0x02} {
		t.Fatalf("MagicV2=%x", MagicV2)
	}
	if MagicV1 != [MagicSize]byte{'W', 'B', '1', 0x01} {
		t.Fatalf("MagicV1=%x", MagicV1)
	}
}

func TestV2PackUnpackRoundTrip(t *testing.T) {
	key := testKey(t)
	plain := Frame{
		Type:     TypeData,
		StreamID: 42,
		Dest:     testSID(1),
		Src:      testSID(2),
		Payload:  []byte("hello v2"),
	}
	wire, err := Pack(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire[:MagicSize], Magic[:]) {
		t.Fatalf("magic %x", wire[:MagicSize])
	}
	if wire[MagicSize] != TypeData {
		t.Fatalf("type %d", wire[MagicSize])
	}
	got, err := Unpack(key, wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != plain.Type || got.StreamID != plain.StreamID {
		t.Fatalf("header %+v vs %+v", got, plain)
	}
	if got.Dest != plain.Dest || got.Src != plain.Src {
		t.Fatalf("route dest=%x src=%x want dest=%x src=%x", got.Dest, got.Src, plain.Dest, plain.Src)
	}
	if !bytes.Equal(got.Payload, plain.Payload) {
		t.Fatalf("payload %q vs %q", got.Payload, plain.Payload)
	}
}

func TestV2UnpackRejectsAADTamperDest(t *testing.T) {
	key := testKey(t)
	wire, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: testSID(1), Src: testSID(2), Payload: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	_, dest, src, ok := PeekRoute(wire)
	if !ok || dest != testSID(1) || src != testSID(2) {
		t.Fatalf("peek dest=%x src=%x ok=%v", dest, src, ok)
	}
	wire[MagicSize+1+2] ^= 0xff
	if _, err := Unpack(key, wire); err == nil {
		t.Fatal("tampered dest must fail AEAD")
	}
	_, dest2, _, ok := PeekRoute(wire)
	if !ok || dest2 == dest {
		t.Fatal("peek must still see tampered dest")
	}
}

func TestV2UnpackRejectsAADTamperSrc(t *testing.T) {
	key := testKey(t)
	wire, err := Pack(key, Frame{Type: TypeOpen, StreamID: 3, Dest: testSID(9), Src: testSID(8), Payload: []byte("h:1")})
	if err != nil {
		t.Fatal(err)
	}
	wire[MagicSize+1+2+SIDSize] ^= 0xff
	if _, err := Unpack(key, wire); err == nil {
		t.Fatal("tampered src must fail AEAD")
	}
}

func TestV2UnpackRejectsAADTamperType(t *testing.T) {
	key := testKey(t)
	wire, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: testSID(1), Src: testSID(2), Payload: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	wire[MagicSize] = TypeFin
	if _, err := Unpack(key, wire); err == nil {
		t.Fatal("tampered type must fail AEAD")
	}
}

func TestPeekRouteBeforeDecrypt(t *testing.T) {
	key := testKey(t)
	dest, src := testSID(4), testSID(5)
	wire, err := Pack(key, Frame{Type: TypeHello, Dest: dest, Src: src})
	if err != nil {
		t.Fatal(err)
	}
	typ, gotDest, gotSrc, ok := PeekRoute(wire)
	if !ok {
		t.Fatal("peek failed")
	}
	if typ != TypeHello || gotDest != dest || gotSrc != src {
		t.Fatalf("typ=%d dest=%x src=%x", typ, gotDest, gotSrc)
	}
}

func TestPeekRouteRejectsV1(t *testing.T) {
	v1 := make([]byte, 32)
	copy(v1[:4], MagicV1[:])
	v1[4] = TypeData
	if _, _, _, ok := PeekRoute(v1); ok {
		t.Fatal("v1 must not peek as v3")
	}
}

func TestPeekRouteRejectsV2(t *testing.T) {
	v2 := make([]byte, 32)
	copy(v2[:4], MagicV2[:])
	v2[4] = TypeData
	if _, _, _, ok := PeekRoute(v2); ok {
		t.Fatal("v2 must not peek as v3")
	}
}

func TestUnpackRejectsV1Magic(t *testing.T) {
	key := testKey(t)
	v1 := make([]byte, 64)
	copy(v1[:4], MagicV1[:])
	v1[4] = TypeData
	_, err := Unpack(key, v1)
	if err == nil {
		t.Fatal("v1 must be rejected")
	}
	if err != errV1Frame {
		t.Fatalf("err=%v want errV1Frame", err)
	}
}

func TestUnpackRejectsV2Magic(t *testing.T) {
	key := testKey(t)
	v2 := make([]byte, 64)
	copy(v2[:4], MagicV2[:])
	v2[4] = TypeData
	_, err := Unpack(key, v2)
	if err == nil {
		t.Fatal("v2 must be rejected as explicit version error")
	}
	if err != errV2Frame {
		t.Fatalf("err=%v want errV2Frame", err)
	}
}

func TestReliabilityFieldsPackUnpack(t *testing.T) {
	key := testKey(t)
	plain := Frame{
		Type:     TypeData,
		StreamID: 7,
		Dest:     testSID(1),
		Src:      testSID(2),
		Epoch:    0xA1B2C3D4,
		Seq:      99,
		Payload:  []byte("rel"),
	}
	wire, err := Pack(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unpack(key, wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.Epoch != plain.Epoch || got.Seq != plain.Seq {
		t.Fatalf("epoch/seq got %d/%d want %d/%d", got.Epoch, got.Seq, plain.Epoch, plain.Seq)
	}
	if !bytes.Equal(got.Payload, plain.Payload) {
		t.Fatalf("payload %q", got.Payload)
	}
}

func TestAckTypeDefined(t *testing.T) {
	if TypeAck == 0 || TypeAck == TypeHello || TypeAck == TypeData || TypeAck == TypePing {
		t.Fatalf("TypeAck=%d", TypeAck)
	}
}

func TestPackRejectsZeroDestForData(t *testing.T) {
	key := testKey(t)
	_, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Src: testSID(1), Payload: []byte("x")})
	if err == nil {
		t.Fatal("zero dest data must fail")
	}
	_, err = Pack(key, Frame{Type: TypeOpen, StreamID: 1, Src: testSID(1), Payload: []byte("h:1")})
	if err == nil {
		t.Fatal("zero dest open must fail")
	}
	_, err = Pack(key, Frame{Type: TypeHello, Src: testSID(1)})
	if err == nil {
		t.Fatal("zero dest hello must fail")
	}
}

func TestPackRejectsZeroSource(t *testing.T) {
	key := testKey(t)
	_, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: testSID(1), Payload: []byte("x")})
	if err == nil {
		t.Fatal("zero source must fail")
	}
}

func TestPackAllowsZeroDestForBeacon(t *testing.T) {
	key := testKey(t)
	sid := testSID(7)
	payload := []byte(FormatBeaconPayload("panel-id", sid))
	wire, err := Pack(key, Frame{Type: TypePing, Dest: SessionID{}, Src: sid, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	typ, dest, src, ok := PeekRoute(wire)
	if !ok || typ != TypePing || !dest.IsZero() || src != sid {
		t.Fatalf("beacon route typ=%d dest=%x src=%x ok=%v", typ, dest, src, ok)
	}
	got, err := Unpack(key, wire)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != string(payload) {
		t.Fatalf("payload %q", got.Payload)
	}
}

func TestNewSessionIDRandom(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		id, err := NewSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if id.IsZero() {
			t.Fatal("sid must not be zero")
		}
		h := hex.EncodeToString(id[:])
		if seen[h] {
			t.Fatalf("duplicate sid %s", h)
		}
		seen[h] = true
		parsed, err := ParseSessionIDHex(id.Hex())
		if err != nil || parsed != id {
			t.Fatalf("hex roundtrip %v %x", err, parsed)
		}
	}
}

func TestHelloTypeDefined(t *testing.T) {
	if TypeHello == 0 || TypeHello == TypePing || TypeHello == TypeData {
		t.Fatalf("TypeHello=%d", TypeHello)
	}
}
