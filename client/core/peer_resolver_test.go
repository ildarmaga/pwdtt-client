package core

import (
	"errors"
	"net"
	"testing"
)

func TestPeerRecoveryWithoutTunnelDNS(t *testing.T) {
	var r peerResolver
	addr := "server.example:46000"
	first, err := r.resolve(addr, false, func(_, _ string) (*net.UDPAddr, error) {
		return &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 46000}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first.IP[0] ^= 1 // Callers must not corrupt the retained address.
	dnsDown := errors.New("DNS points into disconnected tunnel")
	failedLookup := func(_, _ string) (*net.UDPAddr, error) { return nil, dnsDown }
	recovered, err := r.resolve(addr, true, failedLookup)
	if err != nil || recovered.IP.String() != "192.0.2.1" || recovered.Port != 46000 {
		t.Fatalf("recovery = %v, %v", recovered, err)
	}
	recovered.IP[0] ^= 1
	again, err := r.resolve(addr, true, failedLookup)
	if err != nil || again.IP.String() != "192.0.2.1" {
		t.Fatalf("cache mutated: %v, %v", again, err)
	}
	if _, err := r.resolve("other.example:46000", true, failedLookup); !errors.Is(err, dnsDown) {
		t.Fatalf("another server must not reuse this address: %v", err)
	}
	if _, err := r.resolve(addr, false, failedLookup); !errors.Is(err, dnsDown) {
		t.Fatalf("fresh connection must resolve DNS: %v", err)
	}
	_, err = r.resolve(addr, false, func(_, _ string) (*net.UDPAddr, error) {
		return &net.UDPAddr{IP: net.IPv4(192, 0, 2, 2), Port: 46000}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := r.resolve(addr, true, failedLookup)
	if err != nil || updated.IP.String() != "192.0.2.2" {
		t.Fatalf("fresh result not retained: %v, %v", updated, err)
	}
}
