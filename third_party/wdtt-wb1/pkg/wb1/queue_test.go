package wb1

import "testing"

func TestControlLaneCoalescesAckWhenFull(t *testing.T) {
	key := testKey(t)
	q := newPacketQueue(4, 2)
	ping := func(n byte) []byte {
		w, err := Pack(key, Frame{Type: TypePing, Dest: testSID(1), Src: testSID(2), Payload: []byte{n}})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	ack := func(cum uint32) []byte {
		w, err := Pack(key, Frame{Type: TypeAck, Dest: testSID(1), Src: testSID(2), Payload: packAckPayload(cum, 0, 8)})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	if !q.Push(ping(1)) || !q.Push(ping(2)) {
		t.Fatal("seed pings")
	}
	if q.Push(ping(3)) {
		t.Fatal("ctrl cap should drop newest ping")
	}
	if !q.Push(ack(10)) {
		t.Fatal("ACK should replace/coalesce into full control lane")
	}
	if !q.Push(ack(20)) {
		t.Fatal("second ACK should coalesce, not drop newest")
	}
	var acks int
	var lastCum uint32
	n := 0
	for {
		b, ok := q.Pop()
		if !ok {
			break
		}
		n++
		typ, _, _, ok := PeekRoute(b)
		if !ok {
			t.Fatal("peek")
		}
		if typ == TypeAck {
			acks++
			f, err := Unpack(key, b)
			if err != nil {
				t.Fatal(err)
			}
			cum, _, _, ok := unpackAckPayload(f.Payload)
			if !ok {
				t.Fatal("ack payload")
			}
			lastCum = cum
		}
	}
	if acks != 1 || lastCum != 20 {
		t.Fatalf("coalesce: acks=%d cum=%d n=%d", acks, lastCum, n)
	}
	if n > 2 {
		t.Fatalf("control lane grew unbounded: %d", n)
	}
}

func TestControlLaneFairnessDrainsData(t *testing.T) {
	key := testKey(t)
	q := newPacketQueue(8, 8)
	for i := 0; i < 5; i++ {
		w, err := Pack(key, Frame{Type: TypePing, Dest: testSID(1), Src: testSID(2), Payload: []byte{byte(i)}})
		if err != nil {
			t.Fatal(err)
		}
		if !q.Push(w) {
			t.Fatal("ping")
		}
	}
	data, err := Pack(key, Frame{Type: TypeData, Dest: testSID(1), Src: testSID(2), Payload: []byte("d")})
	if err != nil {
		t.Fatal(err)
	}
	if !q.Push(data) {
		t.Fatal("data")
	}
	var types []byte
	for i := 0; i < 6; i++ {
		b, ok := q.Pop()
		if !ok {
			t.Fatal("short queue")
		}
		typ, _, _, ok := PeekRoute(b)
		if !ok {
			t.Fatal("peek")
		}
		types = append(types, typ)
	}
	if types[ctrlFairLimit] != TypeData {
		t.Fatalf("fairness: data not drained after %d ctrl, types=%v", ctrlFairLimit, types)
	}
	if types[0] != TypePing || types[5] != TypePing {
		t.Fatalf("expected remaining ping after data drain, types=%v", types)
	}
}
