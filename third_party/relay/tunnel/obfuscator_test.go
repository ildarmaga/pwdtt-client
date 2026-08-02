package tunnel

import (
	"testing"
	"time"
)

func TestObfuscatorIgnoresStaleEpochAfterAdvance(t *testing.T) {
	obf, err := NewTunnelObfuscator([]byte("room-secret"))
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Unix(0, 0)}
	obf.nowFn = clock.now

	peerA := obfLocalEpoch(0xAAAAAAAA)
	kA := peerA.EncodeKeepalive()
	res := obf.Decode(kA)
	if !res.HasFrame || res.PeerRestart {
		t.Fatalf("first peer epoch: HasFrame=%v PeerRestart=%v", res.HasFrame, res.PeerRestart)
	}

	// Peer A goes silent, then B appears — a real reconnect: hand over once.
	clock.advance(peerHandoverSilence + time.Second)
	peerB := obfLocalEpoch(0xBBBBBBBB)
	kB := peerB.EncodeKeepalive()
	res = obf.Decode(kB)
	if !res.PeerRestart {
		t.Fatal("expected PeerRestart on new epoch after silence")
	}

	for i := 0; i < 5; i++ {
		res = obf.Decode(kA)
		if res.PeerRestart {
			t.Fatalf("stale epoch A must not restart (iter %d)", i)
		}
	}
}

// TestObfuscatorStickyPeerNoStormWhileActive reproduces the two-publisher
// scenario (two devices sharing one room): epochs A and B both keep sending.
// The obfuscator must lock onto the first peer and never storm restarts.
func TestObfuscatorStickyPeerNoStormWhileActive(t *testing.T) {
	obf, err := NewTunnelObfuscator([]byte("room-secret"))
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Unix(0, 0)}
	obf.nowFn = clock.now

	kA := obfLocalEpoch(0x1e493b4d).EncodeKeepalive()
	kB := obfLocalEpoch(0x1b5a86ad).EncodeKeepalive()

	if res := obf.Decode(kA); res.PeerRestart {
		t.Fatal("first peer must not restart")
	}
	restarts := 0
	// Interleave both publishers rapidly (each 50ms apart) for ~10s.
	for i := 0; i < 200; i++ {
		clock.advance(50 * time.Millisecond)
		frame := kA
		if i%2 == 1 {
			frame = kB
		}
		if obf.Decode(frame).PeerRestart {
			restarts++
		}
	}
	if restarts != 0 {
		t.Fatalf("sticky peer stormed: %d restarts (want 0)", restarts)
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func obfLocalEpoch(epoch uint32) *TunnelObfuscator {
	obf, err := NewTunnelObfuscator([]byte("peer-secret"))
	if err != nil {
		panic(err)
	}
	obf.localEpoch = epoch
	return obf
}
