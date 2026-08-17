package wb1

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

func newTestSession(local SessionID) *RoomSession {
	return &RoomSession{
		localSID: local,
		peers:    make(map[string]*Peer),
		joiners:  make(map[SessionID]*sidPipe),
		incoming: newPacketQueue(16, 8),
		done:     make(chan struct{}),
		pacer:    newBytePacer(float64(pacerWrappedRateBits)/8, pacerBurstBytes),
	}
}

func TestWrapUnwrapVP8V2(t *testing.T) {
	key := testKey(t)
	dest, src := testSID(3), testSID(4)
	frame, err := Pack(key, Frame{Type: TypeData, StreamID: 9, Dest: dest, Src: src, Payload: []byte("vp8")})
	if err != nil {
		t.Fatal(err)
	}
	wire := WrapVP8(dest, frame)
	gotDest, gotFrame, ok := UnwrapVP8(wire)
	if !ok {
		t.Fatal("unwrap failed")
	}
	if gotDest != dest {
		t.Fatalf("wrapper dest %x vs %x", gotDest, dest)
	}
	_, envDest, envSrc, ok := PeekRoute(gotFrame)
	if !ok || envDest != dest || envSrc != src {
		t.Fatalf("envelope dest=%x src=%x", envDest, envSrc)
	}
	got, err := Unpack(key, gotFrame)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != "vp8" {
		t.Fatalf("payload %q", got.Payload)
	}
}

func TestBeaconParsesCreatorSIDIgnoresStaleE821(t *testing.T) {
	key := testKey(t)
	creatorSID := testSID(0xAB)
	payload := FormatBeaconPayload("panel-id-1", creatorSID)
	wire, err := Pack(key, Frame{Type: TypePing, Dest: SessionID{}, Src: creatorSID, Payload: []byte(payload)})
	if err != nil {
		t.Fatal(err)
	}

	s := newTestSession(testSID(9))
	s.SetCryptoKey(key)
	ghost := &Peer{Identity: "e8214196-92a9-4fa9-a95c-3cd1cdbef491", Name: "WDTT", Meta: "wdtt-wb1-creator", recv: make(chan []byte, 2), sess: s}
	s.peers[ghost.Identity] = ghost

	s.dispatch("e8214196-92a9-4fa9-a95c-3cd1cdbef491", wire)

	if s.CreatorSID() != creatorSID {
		t.Fatalf("creatorSID %x want %x", s.CreatorSID(), creatorSID)
	}
	if s.beaconID != "panel-id-1" {
		t.Fatalf("beaconID %q", s.beaconID)
	}
	p := s.creatorPeer()
	if p == nil || p.Identity != "panel-id-1" {
		t.Fatalf("creator peer %#v, leftover e821 must not win", p)
	}
}

func TestSameSenderIdentityRoutesBySourceSID(t *testing.T) {
	key := testKey(t)
	creatorSID, aSID, bSID := testSID(1), testSID(2), testSID(3)
	s := newTestSession(creatorSID)
	s.isCreator.Store(1)
	s.SetCryptoKey(key)

	got := map[SessionID][]byte{}
	var gotMu sync.Mutex
	done := make(chan SessionID, 2)
	s.SetOnJoiner(func(sid SessionID, c Carrier) {
		go func(sid SessionID, c Carrier) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			b, err := c.Recv(ctx)
			if err != nil {
				return
			}
			f, err := Unpack(key, b)
			if err != nil {
				return
			}
			gotMu.Lock()
			got[sid] = f.Payload
			gotMu.Unlock()
			done <- sid
		}(sid, c)
	})

	frameA, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: creatorSID, Src: aSID, Payload: []byte("from-A")})
	if err != nil {
		t.Fatal(err)
	}
	frameB, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: creatorSID, Src: bSID, Payload: []byte("from-B")})
	if err != nil {
		t.Fatal(err)
	}
	s.dispatch("e8214196", frameA)
	s.dispatch("e8214196", frameB)

	seen := map[SessionID]bool{}
	for i := 0; i < 2; i++ {
		select {
		case sid := <-done:
			seen[sid] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for routed frames")
		}
	}
	gotMu.Lock()
	defer gotMu.Unlock()
	if string(got[aSID]) != "from-A" || string(got[bSID]) != "from-B" {
		t.Fatalf("crossed streams: A=%q B=%q", got[aSID], got[bSID])
	}
}

func TestJoinerIngestIgnoresOtherDest(t *testing.T) {
	key := testKey(t)
	me, other, creator := testSID(10), testSID(11), testSID(1)
	s := newTestSession(me)
	s.creatorSID = creator
	s.SetCryptoKey(key)

	foreign, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: other, Src: creator, Payload: []byte("other-user")})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(foreign)
	if b, ok := s.incoming.Pop(); ok {
		t.Fatalf("joiner took foreign dest payload %q", b)
	}

	mine, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: me, Src: creator, Payload: []byte("mine")})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(mine)
	b, ok := s.incoming.Pop()
	if !ok {
		t.Fatal("missing own frame")
	}
	f, err := Unpack(key, b)
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Payload) != "mine" {
		t.Fatalf("got %q", f.Payload)
	}
}

func TestMuxPayloadNotOnDataTopic(t *testing.T) {
	data, vp8 := publishPlan(testSID(2), TypeData)
	if data {
		t.Fatal("mux data must not use LiveKit data topic")
	}
	if !vp8 {
		t.Fatal("mux data must use VP8")
	}
	data, vp8 = publishPlan(SessionID{}, TypePing)
	if !data || !vp8 {
		t.Fatal("creator beacon must use both data topic and VP8")
	}
	data, vp8 = publishPlan(testSID(2), TypeAck)
	if !data || !vp8 {
		t.Fatal("ACK must use data topic and VP8 fallback when WB drops data packets")
	}
	data, vp8 = publishPlan(testSID(2), TypePing)
	if !data || !vp8 {
		t.Fatal("mux Ping must use data topic and VP8 fallback")
	}
	data, vp8 = publishPlan(testSID(2), TypePong)
	if !data || !vp8 {
		t.Fatal("Pong must use data topic and VP8 fallback")
	}
}

func TestAckRidesVP8WhenDataTopicSilent(t *testing.T) {
	key := testKey(t)
	s := newTestSession(testSID(1))
	var acks int
	s.writeHook = func(data []byte, _ time.Duration) error {
		_, frame, ok := UnwrapVP8(data)
		if !ok {
			return nil
		}
		typ, _, _, ok := PeekRoute(frame)
		if ok && typ == TypeAck {
			acks++
		}
		return nil
	}
	wire, err := Pack(key, Frame{
		Type: TypeAck, Dest: testSID(2), Src: testSID(1),
		Payload: packAckPayload(1, 0, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publishWire(context.Background(), testSID(2), wire); err != nil {
		t.Fatalf("ACK must succeed via VP8 when LiveKit data topic is down: %v", err)
	}
	if acks == 0 {
		t.Fatal("ACK vanished: data topic silent and VP8 was skipped")
	}
}

func TestControlVP8NotBlockedBySlowDataTopic(t *testing.T) {
	key := testKey(t)
	s := newTestSession(testSID(1))
	s.dataHook = func([]byte) error {
		time.Sleep(2 * time.Second)
		return nil
	}
	var acks int
	s.writeHook = func(data []byte, _ time.Duration) error {
		_, frame, ok := UnwrapVP8(data)
		if !ok {
			return nil
		}
		typ, _, _, ok := PeekRoute(frame)
		if ok && typ == TypeAck {
			acks++
		}
		return nil
	}
	wire, err := Pack(key, Frame{
		Type: TypeAck, Dest: testSID(2), Src: testSID(1),
		Payload: packAckPayload(1, 0, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- s.publishWire(context.Background(), testSID(2), wire)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("ACK send blocked on LiveKit data topic; Recv loop would stall and close mux")
	}
	if acks == 0 {
		t.Fatal("ACK must ride VP8 before waiting on data topic")
	}
}

func TestIdleJoinerNotReapedAfterOutboundSend(t *testing.T) {
	creatorSID := testSID(1)
	s := newTestSession(creatorSID)
	oldSID := testSID(2)
	p := newSIDPipe(s, oldSID)
	p.lastSeen.Store(time.Now().Add(-time.Minute).UnixNano())
	s.joiners[oldSID] = p
	_ = p.Send(context.Background(), []byte{0})
	s.reapIdleJoiners(time.Now(), joinerIdleTimeout)
	if len(s.joiners) != 1 {
		t.Fatalf("outbound send must keep joiner, got %d", len(s.joiners))
	}
}

func TestCreatorMuxCap32(t *testing.T) {
	key := testKey(t)
	creatorSID := testSID(1)
	s := newTestSession(creatorSID)
	s.isCreator.Store(1)
	s.SetCryptoKey(key)
	n := 0
	s.SetOnJoiner(func(sid SessionID, c Carrier) {
		n++
	})
	for i := 0; i < MaxJoiners+1; i++ {
		var src SessionID
		binary.BigEndian.PutUint32(src[:], uint32(i+1))
		wire, err := Pack(key, Frame{Type: TypeHello, Dest: creatorSID, Src: src})
		if err != nil {
			t.Fatal(err)
		}
		s.ingest(wire)
	}
	if n != MaxJoiners {
		t.Fatalf("joiners %d want %d", n, MaxJoiners)
	}
}

func TestIdleJoinerReapedAndSlotReusable(t *testing.T) {
	creatorSID := testSID(1)
	s := newTestSession(creatorSID)
	gone := make(chan SessionID, 1)
	s.SetOnJoinerGone(func(sid SessionID) { gone <- sid })

	oldSID := testSID(2)
	p := newSIDPipe(s, oldSID)
	p.lastSeen.Store(time.Now().Add(-time.Minute).UnixNano())
	s.joiners[oldSID] = p
	s.reapIdleJoiners(time.Now(), joinerIdleTimeout)

	if len(s.joiners) != 0 {
		t.Fatalf("stale joiner not removed: %d", len(s.joiners))
	}
	select {
	case sid := <-gone:
		if sid != oldSID {
			t.Fatalf("gone sid %x", sid)
		}
	default:
		t.Fatal("missing joiner gone callback")
	}
	select {
	case <-p.closed:
	default:
		t.Fatal("stale pipe not closed")
	}
}

func TestCreatorSIDPinnedAfterFirstBeacon(t *testing.T) {
	s := newTestSession(testSID(9))
	first, second := testSID(1), testSID(2)
	s.noteBeaconCreator("creator-a", first)
	s.noteBeaconCreator("creator-b", second)
	if s.CreatorSID() != first {
		t.Fatalf("creator SID changed to %x", s.CreatorSID())
	}
	if s.beaconID != "creator-a" {
		t.Fatalf("creator identity changed to %q", s.beaconID)
	}
}

func TestCreatorRejectsUnauthenticatedRouteBeforeAllocatingMux(t *testing.T) {
	key := testKey(t)
	creatorSID, joinerSID := testSID(1), testSID(2)
	s := newTestSession(creatorSID)
	s.isCreator.Store(1)
	s.SetCryptoKey(key)

	wire, err := Pack(key, Frame{Type: TypeHello, Dest: creatorSID, Src: joinerSID})
	if err != nil {
		t.Fatal(err)
	}
	wire[len(wire)-1] ^= 0xff
	s.ingest(wire)

	if len(s.joiners) != 0 {
		t.Fatalf("unauthenticated route allocated %d mux(es)", len(s.joiners))
	}
}

func TestCreatorDoesNotAllocateMuxForUnknownAck(t *testing.T) {
	key := testKey(t)
	creatorSID, joinerSID := testSID(1), testSID(2)
	s := newTestSession(creatorSID)
	s.isCreator.Store(1)
	s.SetCryptoKey(key)

	wire, err := Pack(key, Frame{Type: TypeAck, Dest: creatorSID, Src: joinerSID, Payload: packAckPayload(1, 0, ARQWindow)})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(wire)
	if len(s.joiners) != 0 {
		t.Fatalf("unknown ACK allocated %d mux(es)", len(s.joiners))
	}

	hello, err := Pack(key, Frame{Type: TypeHello, Dest: creatorSID, Src: joinerSID})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(hello)
	if len(s.joiners) != 1 {
		t.Fatalf("valid Hello allocated %d mux(es), want 1", len(s.joiners))
	}
}

func TestKnownSIDAckTouchesIdleReap(t *testing.T) {
	key := testKey(t)
	creatorSID, joinerSID := testSID(1), testSID(2)
	s := newTestSession(creatorSID)
	s.isCreator.Store(1)
	s.SetCryptoKey(key)
	p := newSIDPipe(s, joinerSID)
	old := time.Now().Add(-time.Minute).UnixNano()
	p.lastSeen.Store(old)
	s.joiners[joinerSID] = p

	wire, err := Pack(key, Frame{Type: TypeAck, Dest: creatorSID, Src: joinerSID, Payload: packAckPayload(1, 0, ARQWindow)})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(wire)
	if p.lastSeen.Load() <= old {
		t.Fatal("ACK on known SID must touch idle timestamp")
	}
}

func TestIncomingQueueDoesNotDropOldest(t *testing.T) {
	key := testKey(t)
	me, creator := testSID(10), testSID(1)
	s := newTestSession(me)
	s.creatorSID = creator
	s.SetCryptoKey(key)

	n := s.incoming.dataCap
	if n < 2 {
		t.Fatalf("incoming cap %d", n)
	}
	for i := 0; i < n; i++ {
		wire, err := Pack(key, Frame{
			Type: TypeData, StreamID: 1, Dest: me, Src: creator,
			Payload: []byte{byte(i)},
		})
		if err != nil {
			t.Fatal(err)
		}
		s.ingest(wire)
	}
	overflow, err := Pack(key, Frame{
		Type: TypeData, StreamID: 1, Dest: me, Src: creator,
		Payload: []byte{0xff},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(overflow)

	raw, ok := s.incoming.Pop()
	if !ok {
		t.Fatal("empty incoming")
	}
	first, err := Unpack(key, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Payload) != 1 || first.Payload[0] != 0 {
		t.Fatalf("oldest reliable packet dropped, first=%v", first.Payload)
	}
	sawOverflow := false
	for i := 0; i < n-1; i++ {
		b, ok := s.incoming.Pop()
		if !ok {
			break
		}
		f, err := Unpack(key, b)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.Payload) == 1 && f.Payload[0] == 0xff {
			sawOverflow = true
		}
	}
	if sawOverflow {
		t.Fatal("newest overflow should be dropped, not oldest")
	}
}

func TestSIDPipeDoesNotDropOldest(t *testing.T) {
	s := newTestSession(testSID(1))
	p := &sidPipe{sess: s, remote: testSID(2), q: newPacketQueue(2, 2), closed: make(chan struct{})}
	p.push([]byte("oldest"))
	p.push([]byte("second"))
	p.push([]byte("newest"))
	a, ok1 := p.q.Pop()
	b, ok2 := p.q.Pop()
	if !ok1 || !ok2 || string(a) != "oldest" || string(b) != "second" {
		t.Fatalf("drop-oldest still in effect: %q %q", a, b)
	}
	if x, ok := p.q.Pop(); ok {
		t.Fatalf("unexpected third packet %q", x)
	}
}

func TestSIDPipeAckBypassesFullDataQueue(t *testing.T) {
	key := testKey(t)
	creatorSID, joinerSID := testSID(1), testSID(2)
	s := newTestSession(creatorSID)
	s.isCreator.Store(1)
	s.SetCryptoKey(key)
	p := &sidPipe{sess: s, remote: joinerSID, q: newPacketQueue(2, 2), closed: make(chan struct{})}
	s.joiners[joinerSID] = p
	for i := 0; i < 2; i++ {
		wire, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: creatorSID, Src: joinerSID, Payload: []byte{byte(i)}})
		if err != nil {
			t.Fatal(err)
		}
		p.push(wire)
	}
	ack, err := Pack(key, Frame{Type: TypeAck, Dest: creatorSID, Src: joinerSID, Payload: packAckPayload(1, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	p.push(ack)
	got, ok := p.q.Pop()
	if !ok {
		t.Fatal("queue empty after ACK inject")
	}
	typ, _, _, ok := PeekRoute(got)
	if !ok || typ != TypeAck {
		t.Fatalf("ACK dropped behind full data queue, typ=%d ok=%v", typ, ok)
	}
	d0, ok0 := p.q.Pop()
	d1, ok1 := p.q.Pop()
	if !ok0 || !ok1 {
		t.Fatal("queued data lost after ACK bypass")
	}
	f0, err := Unpack(key, d0)
	if err != nil {
		t.Fatal(err)
	}
	f1, err := Unpack(key, d1)
	if err != nil {
		t.Fatal(err)
	}
	if f0.Type != TypeData || f1.Type != TypeData || f0.Payload[0] != 0 || f1.Payload[0] != 1 {
		t.Fatalf("data order after ACK: %v %v", f0.Payload, f1.Payload)
	}
}

func TestIncomingAckBypassesFullDataQueue(t *testing.T) {
	key := testKey(t)
	me, creator := testSID(10), testSID(1)
	s := newTestSession(me)
	s.creatorSID = creator
	s.SetCryptoKey(key)
	n := s.incoming.dataCap
	for i := 0; i < n; i++ {
		wire, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: me, Src: creator, Payload: []byte{byte(i)}})
		if err != nil {
			t.Fatal(err)
		}
		s.ingest(wire)
	}
	ack, err := Pack(key, Frame{Type: TypeAck, Dest: me, Src: creator, Payload: packAckPayload(3, 0, 8)})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(ack)
	got, ok := s.incoming.Pop()
	if !ok {
		t.Fatal("incoming empty")
	}
	typ, _, _, ok := PeekRoute(got)
	if !ok || typ != TypeAck {
		t.Fatalf("joiner incoming dropped ACK on full data queue typ=%d", typ)
	}
}

func TestIngestDropsV2(t *testing.T) {
	s := newTestSession(testSID(1))
	s.isCreator.Store(1)
	started := false
	s.SetOnJoiner(func(sid SessionID, c Carrier) { started = true })
	v2 := make([]byte, 64)
	copy(v2[:4], MagicV2[:])
	v2[4] = TypeHello
	s.ingest(v2)
	if started {
		t.Fatal("v2 must not start a mux")
	}
}

func TestCreatorDoesNotAllocateMuxForUnknownPong(t *testing.T) {
	key := testKey(t)
	creatorSID, joinerSID := testSID(1), testSID(2)
	s := newTestSession(creatorSID)
	s.isCreator.Store(1)
	s.SetCryptoKey(key)

	wire, err := Pack(key, Frame{Type: TypePong, Dest: creatorSID, Src: joinerSID, Payload: make([]byte, 8)})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(wire)

	if len(s.joiners) != 0 {
		t.Fatalf("unknown pong allocated %d mux(es)", len(s.joiners))
	}
}

func TestSetOnPeerDoesNotStartMux(t *testing.T) {
	s := newTestSession(testSID(1))
	s.isCreator.Store(1)
	muxStarted := false
	s.SetOnJoiner(func(sid SessionID, c Carrier) {
		muxStarted = true
	})
	s.SetOnPeer(func(p *Peer) {})
	p := s.ensurePeer("livekit-joiner", "PWDTT", "")
	if p == nil {
		t.Fatal("peer")
	}
	if muxStarted {
		t.Fatal("LiveKit identity must not start a mux")
	}
}

func TestParseBeaconPayload(t *testing.T) {
	sid := testSID(0xCD)
	p := FormatBeaconPayload("abc", sid)
	id, got, ok := ParseBeaconPayload(p)
	if !ok || id != "abc" || got != sid {
		t.Fatalf("id=%q sid=%x ok=%v", id, got, ok)
	}
	if _, _, ok := ParseBeaconPayload(CreatorBeaconPayload + "|only-id"); ok {
		t.Fatal("v1 two-part beacon must not parse as v2 SID")
	}
}

func TestWaitCreatorUsesBeaconSID(t *testing.T) {
	key := testKey(t)
	creatorSID := testSID(0x42)
	s := newTestSession(testSID(9))
	s.SetCryptoKey(key)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		p, err := s.WaitCreator(ctx)
		if err != nil {
			errCh <- err
			return
		}
		if p == nil || p.Identity != "panel-id-1" {
			errCh <- context.Canceled
			return
		}
		errCh <- nil
	}()
	time.Sleep(20 * time.Millisecond)
	wire, err := Pack(key, Frame{Type: TypePing, Dest: SessionID{}, Src: creatorSID, Payload: []byte(FormatBeaconPayload("panel-id-1", creatorSID))})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(wire)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("WaitCreator")
	}
	if s.CreatorSID() != creatorSID {
		t.Fatalf("sid %x", s.CreatorSID())
	}
}

func TestIngestDropsV1(t *testing.T) {
	s := newTestSession(testSID(1))
	s.isCreator.Store(1)
	started := false
	s.SetOnJoiner(func(sid SessionID, c Carrier) { started = true })
	v1 := make([]byte, 64)
	copy(v1[:4], MagicV1[:])
	v1[4] = TypeData
	s.ingest(v1)
	if started {
		t.Fatal("v1 must not start a mux")
	}
}

func TestSetOnJoinerFlushesPending(t *testing.T) {
	key := testKey(t)
	creatorSID, src := testSID(1), testSID(2)
	s := newTestSession(creatorSID)
	s.isCreator.Store(1)
	s.SetCryptoKey(key)
	wire, err := Pack(key, Frame{Type: TypeHello, Dest: creatorSID, Src: src})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(wire)
	got := SessionID{}
	s.SetOnJoiner(func(sid SessionID, c Carrier) {
		got = sid
		if c == nil {
			t.Fatal("nil carrier")
		}
	})
	if got != src {
		t.Fatalf("pending sid %x", got)
	}
}
