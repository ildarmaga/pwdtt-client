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

func TestCreatorPeerPrefersSpokeNotGhost(t *testing.T) {
	ghost := &Peer{Identity: "ghost"}
	real := &Peer{Identity: "real"}
	real.spoke.Store(1)
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
