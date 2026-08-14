package wb1

import "testing"

func TestCreatorPeerIgnoresNamedLeftoverWithoutBeacon(t *testing.T) {
	ghost := &Peer{Identity: "e8214196-92a9-4fa9-a95c-3cd1cdbef491", Name: "WDTT", Meta: "wdtt-wb1-creator"}
	s := &RoomSession{peers: map[string]*Peer{ghost.Identity: ghost}}
	if p := s.creatorPeer(); p != nil {
		t.Fatalf("leftover named WDTT must not be creator before beacon: %s", p.Identity)
	}
}

func TestCreatorPeerIgnoresLeftoverBeaconFlag(t *testing.T) {
	ghost := &Peer{Identity: "e8214196", Name: "WDTT", Meta: "wdtt-wb1-creator"}
	ghost.beacon.Store(1)
	s := &RoomSession{peers: map[string]*Peer{ghost.Identity: ghost}}
	if p := s.creatorPeer(); p != nil {
		t.Fatalf("leftover beacon flag must not win without beaconID: %s", p.Identity)
	}
}

func TestLeftoverCreatorPeer(t *testing.T) {
	if !IsLeftoverCreator(&Peer{Identity: "e8214196", Name: "WDTT"}) {
		t.Fatal("named WDTT leftover")
	}
	if !IsLeftoverCreator(&Peer{Identity: "x", Meta: "wdtt-wb1-creator"}) {
		t.Fatal("metadata leftover")
	}
	if IsLeftoverCreator(&Peer{Identity: "70cc629c", Name: ""}) {
		t.Fatal("real joiner with empty name")
	}
}

func TestDispatchLeftoverDoesNotCloneToAllJoiners(t *testing.T) {
	s := &RoomSession{peers: map[string]*Peer{}}
	s.isCreator.Store(1)
	ghost := &Peer{
		Identity: "e8214196", Name: "WDTT", Meta: "wdtt-wb1-creator",
		recv: make(chan []byte, 4), sess: s,
	}
	a := &Peer{Identity: "joiner-a", recv: make(chan []byte, 4), sess: s}
	b := &Peer{Identity: "joiner-b", recv: make(chan []byte, 4), sess: s}
	s.peers[ghost.Identity] = ghost
	s.peers[a.Identity] = a
	s.peers[b.Identity] = b

	s.dispatch("e8214196", []byte("open-from-one-user"))
	select {
	case <-a.recv:
		t.Fatal("leftover stamp must not be copied to joiner A")
	default:
	}
	select {
	case <-b.recv:
		t.Fatal("leftover stamp must not be copied to joiner B")
	default:
	}
}

func TestFanInSkipsOtherJoiners(t *testing.T) {
	key, err := DeriveKey("pass", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	me, creator := testSID(10), testSID(1)
	s := newTestSession(me)
	s.creatorSID = creator
	s.SetCryptoKey(key)
	other, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: testSID(11), Src: creator, Payload: []byte("other-socks")})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(other)
	if _, ok := s.incoming.Pop(); ok {
		t.Fatal("other joiner dest must not fan-in")
	}
	mine, err := Pack(key, Frame{Type: TypeData, StreamID: 1, Dest: me, Src: creator, Payload: []byte("creator-data")})
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(mine)
	b, ok := s.incoming.Pop()
	if !ok {
		t.Fatal("own dest frames must be accepted")
	}
	f, err := Unpack(key, b)
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Payload) != "creator-data" {
		t.Fatalf("got %q", f.Payload)
	}
}

func TestCreatorPeerIgnoresSpokeWithoutBeacon(t *testing.T) {
	ghost := &Peer{Identity: "e8214196-ghost"}
	ghost.spoke.Store(1)
	s := &RoomSession{peers: map[string]*Peer{"ghost": ghost}}
	if p := s.creatorPeer(); p != nil {
		t.Fatalf("stale joiner spoke must not be creator: %s", p.Identity)
	}
}

func TestCreatorPeerPrefersBeacon(t *testing.T) {
	ghost := &Peer{Identity: "ghost"}
	ghost.spoke.Store(1)
	real := &Peer{Identity: "real"}
	real.beacon.Store(1)
	s := &RoomSession{
		beaconID: "real",
		peers:    map[string]*Peer{"ghost": ghost, "real": real},
	}
	p := s.creatorPeer()
	if p == nil || p.Identity != "real" {
		t.Fatalf("got %#v", p)
	}
}

func TestCreatorPeerNoUnnamedFallback(t *testing.T) {
	s := &RoomSession{peers: map[string]*Peer{"ghost": {Identity: "ghost"}}}
	if p := s.creatorPeer(); p != nil {
		t.Fatalf("unnamed single peer must not be treated as creator: %s", p.Identity)
	}
}

func TestPeerPushMarksSpoke(t *testing.T) {
	p := &Peer{recv: make(chan []byte, 2)}
	p.push([]byte("x"))
	if p.spoke.Load() == 0 {
		t.Fatal("push should mark spoke")
	}
}

func TestPeerPushBeaconMarksCreator(t *testing.T) {
	key, err := DeriveKey("pass", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Pack(key, Frame{Type: TypePing, Dest: SessionID{}, Src: testSID(1), Payload: []byte(CreatorBeaconPayload)})
	if err != nil {
		t.Fatal(err)
	}
	s := &RoomSession{key: key}
	p := &Peer{Identity: "creator", recv: make(chan []byte, 2), sess: s}
	p.push(wire)
	if !isCreatorBeacon(key, wire) {
		t.Fatal("isCreatorBeacon")
	}
}

func TestCreatorPeerPrefersBeaconIdentityOverSender(t *testing.T) {
	ghost := &Peer{Identity: "e8214196-ghost"}
	ghost.beacon.Store(1)
	real := &Peer{Identity: "panel-creator"}
	s := &RoomSession{
		beaconID: "panel-creator",
		peers:    map[string]*Peer{"e8214196-ghost": ghost, "panel-creator": real},
	}
	p := s.creatorPeer()
	if p == nil || p.Identity != "panel-creator" {
		t.Fatalf("beacon payload identity must win over leftover sender: %#v", p)
	}
}

func TestCreatorBeaconWithIdentity(t *testing.T) {
	key, err := DeriveKey("pass", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	sid := testSID(0x11)
	wire, err := Pack(key, Frame{Type: TypePing, Dest: SessionID{}, Src: sid, Payload: []byte(FormatBeaconPayload("panel-id-1", sid))})
	if err != nil {
		t.Fatal(err)
	}
	if !isCreatorBeacon(key, wire) {
		t.Fatal("prefix beacon")
	}
	if got := creatorIDFromBeacon(key, wire); got != "panel-id-1" {
		t.Fatalf("id %q", got)
	}
	s := &RoomSession{key: key, peers: map[string]*Peer{}, joiners: map[SessionID]*sidPipe{}, incoming: newPacketQueue(4, 2), localSID: testSID(9)}
	ghost := &Peer{Identity: "ghost", recv: make(chan []byte, 2), sess: s}
	s.peers["ghost"] = ghost
	ghost.push(wire)
	if s.beaconID != "panel-id-1" {
		t.Fatalf("beaconID %q", s.beaconID)
	}
	if s.CreatorSID() != sid {
		t.Fatalf("creatorSID %x", s.CreatorSID())
	}
	p := s.creatorPeer()
	if p == nil || p.Identity != "panel-id-1" {
		t.Fatalf("got %#v", p)
	}
}

func TestJoinerPingIsNotCreatorBeacon(t *testing.T) {
	key, err := DeriveKey("pass", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Pack(key, Frame{Type: TypePing, Dest: testSID(1), Src: testSID(2), Payload: []byte{0, 0, 0, 0, 0, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if isCreatorBeacon(key, wire) {
		t.Fatal("mux ping must not look like creator beacon")
	}
}
