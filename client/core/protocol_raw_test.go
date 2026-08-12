package core

import (
	"net"
	"strings"
	"testing"
)

func TestRequestRawConfigOK(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() {
		buf := make([]byte, 256)
		n, err := c2.Read(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) != "RAWCONF:dev1|secret|1280|CHAL1" {
			t.Errorf("payload %q", string(buf[:n]))
		}
		_, _ = c2.Write([]byte("IP = 10.70.66.3\nDNS = 8.8.8.8\nMTU = 1280\n"))
	}()
	resp, err := RequestRawConfig(c1, "dev1", "secret", 1280)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "10.70.66.3") {
		t.Fatalf("resp %q", resp)
	}
}

func TestRequestRawConfigNoConf(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() {
		buf := make([]byte, 64)
		_, _ = c2.Read(buf)
		_, _ = c2.Write([]byte("NOCONF"))
	}()
	_, err := RequestRawConfig(c1, "d", "p", 1280)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRequestRawConfigNegotiatesChunk1(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() {
		buf := make([]byte, 256)
		n, _ := c2.Read(buf)
		if got := string(buf[:n]); got != "RAWCONF:dev1|secret|1160|CHAL1|CHUNK1" {
			t.Errorf("payload %q", got)
		}
		_, _ = c2.Write([]byte("IP = 10.70.66.3\nDNS = 8.8.8.8\nMTU = 1160\nCAPS = CHUNK1\n"))
	}()
	if _, err := RequestRawConfigCapabilities(c1, "dev1", "secret", 1160, true); err != nil {
		t.Fatal(err)
	}
}

func TestRequestRawConfigRejectsMissingChunk1(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() {
		buf := make([]byte, 256)
		_, _ = c2.Read(buf)
		_, _ = c2.Write([]byte("IP = 10.70.66.3\nDNS = 8.8.8.8\nMTU = 1160\n"))
	}()
	_, err := RequestRawConfigCapabilities(c1, "dev1", "secret", 1160, true)
	if err == nil || !strings.Contains(err.Error(), "CHUNK1") {
		t.Fatalf("want explicit CHUNK1 error, got %v", err)
	}
}

func TestRequestRawConfigHandlesChallenge(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	chal := "0123456789abcdef0123456789abcdef"
	go func() {
		buf := make([]byte, 256)
		n, _ := c2.Read(buf)
		if got := string(buf[:n]); got != "RAWCONF:dev1|secret|1160|CHAL1|CHUNK1" {
			t.Errorf("first payload %q", got)
		}
		_, _ = c2.Write([]byte("RAWCHAL:" + chal))
		n, _ = c2.Read(buf)
		want := "RAWCONF:dev1|secret|1160|CHAL1|CHUNK1|CHAL=" + chal
		if got := string(buf[:n]); got != want {
			t.Errorf("chal payload %q want %q", got, want)
		}
		_, _ = c2.Write([]byte("IP = 10.70.66.3\nDNS = 8.8.8.8\nMTU = 1160\nCAPS = CHUNK1\n"))
	}()
	resp, err := RequestRawConfigCapabilities(c1, "dev1", "secret", 1160, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "10.70.66.3") {
		t.Fatalf("resp %q", resp)
	}
}
