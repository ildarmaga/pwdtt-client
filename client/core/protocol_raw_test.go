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
		if string(buf[:n]) != "RAWCONF:dev1|secret|1280" {
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
