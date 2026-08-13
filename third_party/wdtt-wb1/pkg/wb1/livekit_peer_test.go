package wb1

import "testing"

func TestCreatorPeerPrefersNamed(t *testing.T) {
	ghost := &Peer{Identity: "ghost"}
	real := &Peer{Identity: "real", Name: "WDTT"}
	s := &RoomSession{peers: map[string]*Peer{"ghost": ghost, "real": real}}
	p := s.creatorPeer()
	if p == nil || p.Identity != "real" {
		t.Fatalf("got %#v", p)
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
	s := &RoomSession{peers: map[string]*Peer{"ghost": ghost, "real": real}}
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
	wire, err := Pack(key, Frame{Type: TypePing, Payload: []byte(CreatorBeaconPayload)})
	if err != nil {
		t.Fatal(err)
	}
	s := &RoomSession{key: key}
	p := &Peer{Identity: "creator", recv: make(chan []byte, 2), sess: s}
	p.push(wire)
	if p.beacon.Load() == 0 {
		t.Fatal("creator beacon must mark beacon")
	}
	if !isCreatorBeacon(key, wire) {
		t.Fatal("isCreatorBeacon")
	}
}

func TestJoinerPingIsNotCreatorBeacon(t *testing.T) {
	key, err := DeriveKey("pass", "room-1")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Pack(key, Frame{Type: TypePing, Payload: []byte{0, 0, 0, 0, 0, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if isCreatorBeacon(key, wire) {
		t.Fatal("mux ping must not look like creator beacon")
	}
}
