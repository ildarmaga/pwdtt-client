package core

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestParseRawConfIP(t *testing.T) {
	ip := parseRawConfIP("IP = 10.70.3.9\nDNS = 1.1.1.1\nMTU = 1280\n")
	if ip.String() != "10.70.3.9" {
		t.Fatalf("got %v", ip)
	}
}

func TestRewriteIPv4SrcDstChecksum(t *testing.T) {
	// Minimal IPv4 + UDP: src 10.70.66.2 -> dst 1.1.1.1, sport 1234 dport 53
	pkt := make([]byte, 28)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], 28)
	pkt[8] = 64
	pkt[9] = 17 // UDP
	copy(pkt[12:16], net.IPv4(10, 70, 66, 2).To4())
	copy(pkt[16:20], net.IPv4(1, 1, 1, 1).To4())
	binary.BigEndian.PutUint16(pkt[20:22], 1234)
	binary.BigEndian.PutUint16(pkt[22:24], 53)
	binary.BigEndian.PutUint16(pkt[24:26], 8)
	fixIPv4Checksums(pkt)
	ipCS := binary.BigEndian.Uint16(pkt[10:12])
	udpCS := binary.BigEndian.Uint16(pkt[26:28])

	if !rewriteIPv4SrcInPlace(pkt, net.IPv4(10, 70, 1, 5)) {
		t.Fatal("src rewrite failed")
	}
	if net.IP(pkt[12:16]).String() != "10.70.1.5" {
		t.Fatalf("src=%v", net.IP(pkt[12:16]))
	}
	if binary.BigEndian.Uint16(pkt[10:12]) == ipCS && binary.BigEndian.Uint16(pkt[26:28]) == udpCS {
		t.Fatal("checksums should change after src rewrite")
	}

	if !rewriteIPv4DstInPlace(pkt, net.IPv4(10, 70, 66, 2)) {
		t.Fatal("dst rewrite failed")
	}
	if net.IP(pkt[16:20]).String() != "10.70.66.2" {
		t.Fatalf("dst=%v", net.IP(pkt[16:20]))
	}
	// Recompute reference and compare
	want := append([]byte(nil), pkt...)
	binary.BigEndian.PutUint16(want[10:12], 0)
	binary.BigEndian.PutUint16(want[26:28], 0)
	fixIPv4Checksums(want)
	if binary.BigEndian.Uint16(pkt[10:12]) != binary.BigEndian.Uint16(want[10:12]) {
		t.Fatalf("ip csum got %x want %x", binary.BigEndian.Uint16(pkt[10:12]), binary.BigEndian.Uint16(want[10:12]))
	}
	if binary.BigEndian.Uint16(pkt[26:28]) != binary.BigEndian.Uint16(want[26:28]) {
		t.Fatalf("udp csum got %x want %x", binary.BigEndian.Uint16(pkt[26:28]), binary.BigEndian.Uint16(want[26:28]))
	}
}
