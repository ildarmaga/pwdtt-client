package wb1

import (
	"errors"
	"testing"
)

func TestValidateFrameSemantics(t *testing.T) {
	tests := []struct {
		name string
		f    Frame
		ok   bool
	}{
		{name: "open", f: Frame{Type: TypeOpen, StreamID: 1, Payload: []byte("example.com:443")}, ok: true},
		{name: "data", f: Frame{Type: TypeData, StreamID: 1}, ok: true},
		{name: "ping", f: Frame{Type: TypePing}, ok: true},
		{name: "unknown type", f: Frame{Type: 255, StreamID: 1}},
		{name: "open stream zero", f: Frame{Type: TypeOpen, Payload: []byte("example.com:443")}},
		{name: "legacy data stream zero", f: Frame{Type: TypeData}, ok: true},
		{name: "legacy ping with stream", f: Frame{Type: TypePing, StreamID: 1}, ok: true},
		{name: "empty destination", f: Frame{Type: TypeOpen, StreamID: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFrameSemantics(tt.f)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateMuxDestination(t *testing.T) {
	for _, dest := range []string{"example.com:443", "1.1.1.1:53", "[2001:db8::1]:443", "udp:udp:1.1.1.1:53"} {
		if err := validateMuxDestination(dest); err != nil {
			t.Fatalf("%q: %v", dest, err)
		}
	}
	for _, dest := range []string{"", "example.com", ":443", "example.com:0", "example.com:65536", "udp:"} {
		if err := validateMuxDestination(dest); err == nil {
			t.Fatalf("%q: expected error", dest)
		}
	}
}

func TestNormalizeNestedUDPDestination(t *testing.T) {
	hostPort, udp, err := normalizeMuxDestination("udp:udp:1.1.1.1:53")
	if err != nil || !udp || hostPort != "1.1.1.1:53" {
		t.Fatalf("got hostPort=%q udp=%v err=%v", hostPort, udp, err)
	}
}

func TestMuxRejectsStreamLimit(t *testing.T) {
	m := NewMux(make([]byte, KeySize), nil)
	m.maxStreams = 1
	m.streams[1] = newStream(m, 1)
	_, err := m.Dial(t.Context(), "example.com:443")
	if !errors.Is(err, errStreamLimit) {
		t.Fatalf("Dial error = %v, want %v", err, errStreamLimit)
	}
}
