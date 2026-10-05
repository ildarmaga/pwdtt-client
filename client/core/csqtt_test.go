package core

import (
	"bytes"
	"testing"
)

func TestCSQTTWrapKeyDiffersFromWDTT(t *testing.T) {
	pass := "test-password-123"
	wdtt, err := deriveWrapKey(pass)
	if err != nil {
		t.Fatal(err)
	}
	csqtt, err := deriveCSQTTWrapKey(pass)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(wdtt, csqtt) {
		t.Fatal("CSQTT and WDTT WRAP keys must differ")
	}
	if len(csqtt) != wrapKeyLen {
		t.Fatalf("key len %d", len(csqtt))
	}
}

func TestCSQTTPeerAddrReplacesPort(t *testing.T) {
	got, err := csqttPeerAddr("94.242.53.211:56000", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "94.242.53.211:46000" {
		t.Fatalf("default port: %s", got)
	}
	got, err = csqttPeerAddr("[2001:db8::1]:56000", 46001)
	if err != nil {
		t.Fatal(err)
	}
	if got != "[2001:db8::1]:46001" {
		t.Fatalf("ipv6: %s", got)
	}
}

func TestCSQTTTunconfBecomesRawConfig(t *testing.T) {
	ip, dns, ok := parseTUNCONF("TUNCONF:10.70.1.8:1.1.1.1:9000")
	if !ok || ip != "10.70.1.8" || dns != "1.1.1.1" {
		t.Fatalf("parse %q %q %v", ip, dns, ok)
	}
	if _, _, ok = parseTUNCONF("DTLS"); ok {
		t.Fatal("DTLS is not a CSQTT config")
	}
	got := formatCSQTTRawConfig(ip, dns, 1280)
	if got != "IP = 10.70.1.8\nDNS = 1.1.1.1\nMTU = 1280\n" {
		t.Fatalf("raw config %q", got)
	}
}

func TestCSQTTGetconfNamesTheMask(t *testing.T) {
	if got := csqttGetconfPayload("9000", "dev", "secret", "audio"); got != "GETCONF:9000|dev|secret|0|Audio" {
		t.Fatalf("audio: %s", got)
	}
	if got := csqttGetconfPayload("9000", "dev", "secret", "video"); got != "GETCONF:9000|dev|secret|0|Video" {
		t.Fatalf("video: %s", got)
	}
}
