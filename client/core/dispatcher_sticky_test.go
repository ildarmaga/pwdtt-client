package core

import "testing"

func TestFlowHashStable(t *testing.T) {
	// IPv4 + TCP 10.70.0.3:1234 -> 1.2.3.4:443
	pkt := []byte{
		0x45, 0, 0, 40, 0, 0, 0, 0, 64, 6, 0, 0,
		10, 70, 0, 3,
		1, 2, 3, 4,
		0x04, 0xd2, // 1234
		0x01, 0xbb, // 443
		0, 0, 0, 0, 0, 0, 0, 0, 0x50, 0x02, 0, 0, 0, 0, 0, 0,
	}
	h1 := flowHash(pkt)
	h2 := flowHash(pkt)
	if h1 != h2 {
		t.Fatal("unstable")
	}
	pkt[15] = 4 // change src host
	if flowHash(pkt) == h1 {
		t.Fatal("src change should change hash")
	}
}
