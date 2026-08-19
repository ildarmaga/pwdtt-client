package backend

import (
	"encoding/binary"
	"net"
	"testing"
)

func testConsumerPacket(src, dst net.IP, srcPort, dstPort uint16) []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	pkt[9] = 6
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	binary.BigEndian.PutUint16(pkt[20:22], srcPort)
	binary.BigEndian.PutUint16(pkt[22:24], dstPort)
	return pkt
}

func TestTunnelConsumersAttributeBothDirections(t *testing.T) {
	resetTunnelConsumers()
	out := testConsumerPacket(net.IPv4(10, 70, 0, 2), net.IPv4(1, 1, 1, 1), 50000, 443)
	in := testConsumerPacket(net.IPv4(1, 1, 1, 1), net.IPv4(10, 70, 0, 2), 443, 50000)
	observeTunnelPacket(out, true)
	observeTunnelPacket(in, false)
	rows := snapshotTunnelConsumers(20)
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1", len(rows))
	}
	if rows[0].Address != "1.1.1.1:443" || rows[0].RxBytes != 40 || rows[0].TxBytes != 40 {
		t.Fatalf("row=%+v", rows[0])
	}
}

func TestTunnelConsumersSortAndLimit(t *testing.T) {
	resetTunnelConsumers()
	for i := 0; i < 3; i++ {
		pkt := testConsumerPacket(net.IPv4(10, 70, 0, 2), net.IPv4(8, 8, 8, byte(i+1)), 50000, 443)
		for n := 0; n <= i; n++ {
			observeTunnelPacket(pkt, true)
		}
	}
	rows := snapshotTunnelConsumers(2)
	if len(rows) != 2 || rows[0].Address != "8.8.8.3:443" || rows[1].Address != "8.8.8.2:443" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestNamedTrafficUsesDomain(t *testing.T) {
	resetTunnelConsumers()
	observeNamedTraffic("example.com:443", 120, 80)
	rows := snapshotTunnelConsumers(20)
	if len(rows) != 1 || rows[0].Domain != "example.com" || rows[0].RxBytes != 120 || rows[0].TxBytes != 80 {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestDNSResponseLabelsTunnelIP(t *testing.T) {
	resetTunnelConsumers()
	name := []byte{7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	msg := make([]byte, 12)
	msg[2] = 0x80
	binary.BigEndian.PutUint16(msg[4:6], 1)
	binary.BigEndian.PutUint16(msg[6:8], 1)
	msg = append(msg, name...)
	msg = append(msg, 0, 1, 0, 1)
	msg = append(msg, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 93, 184, 216, 34)
	observeDNSMessage(msg)
	observeTunnelPacket(testConsumerPacket(net.IPv4(10, 70, 0, 2), net.IPv4(93, 184, 216, 34), 50000, 443), true)
	rows := snapshotTunnelConsumers(20)
	if len(rows) != 1 || rows[0].Domain != "example.com" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestTLSClientHelloServerName(t *testing.T) {
	name := "api.example.com"
	serverName := append([]byte{0, byte(len(name) + 3), 0, 0, byte(len(name))}, []byte(name)...)
	ext := append([]byte{0, 0, byte(len(serverName) >> 8), byte(len(serverName))}, serverName...)
	body := make([]byte, 34)
	body = append(body, 0) // session id
	body = append(body, 0, 2, 0x13, 0x01)
	body = append(body, 1, 0) // compression
	body = append(body, byte(len(ext)>>8), byte(len(ext)))
	body = append(body, ext...)
	handshake := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	handshake = append(handshake, body...)
	record := []byte{22, 3, 1, byte(len(handshake) >> 8), byte(len(handshake))}
	record = append(record, handshake...)
	if got := tlsServerName(record); got != name {
		t.Fatalf("server name=%q want %q", got, name)
	}
}

func TestHTTPHost(t *testing.T) {
	if got := packetDomain([]byte("GET / HTTP/1.1\r\nHost: example.org:80\r\n\r\n")); got != "example.org" {
		t.Fatalf("host=%q", got)
	}
}
