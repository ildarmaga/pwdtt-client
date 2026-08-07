package backend

import "testing"

func TestParseRawConfig(t *testing.T) {
	ip, dns, mtu, err := parseRawConfig("IP = 10.70.66.5\nDNS = 1.1.1.1\nMTU = 1280\n")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "10.70.66.5" || dns != "1.1.1.1" || mtu != 1280 {
		t.Fatalf("got ip=%q dns=%q mtu=%d", ip, dns, mtu)
	}
	ip, _, _, err = parseRawConfig("IP=10.70.66.9/24\n")
	if err != nil || ip != "10.70.66.9" {
		t.Fatalf("cidr strip: ip=%q err=%v", ip, err)
	}
	if _, _, _, err := parseRawConfig("DNS = 1.1.1.1\n"); err == nil {
		t.Fatal("expected error without IP")
	}
}
