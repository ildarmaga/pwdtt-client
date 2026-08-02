package common

import "testing"

func TestIsNonRoutableHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:443":        true,
		"192.168.1.5:1080":     true,
		"10.0.0.1:53":          true,
		"198.18.0.42:443":      true,
		"149.154.175.50:443":   false,
		"1.1.1.1:853":          false,
		"example.com:443":      false,
	}
	for host, want := range cases {
		if got := IsNonRoutableHost(host); got != want {
			t.Fatalf("%q => %v want %v", host, got, want)
		}
	}
}

func TestIsTunnelSinkHost(t *testing.T) {
	sink := map[string]bool{
		"172.31.255.254:443": true,
		"10.99.0.1:443":      true,
		"10.99.0.2:53":       true,
		"149.154.175.50:443": false,
		"192.168.1.1:443":    false,
	}
	for host, want := range sink {
		if got := IsTunnelSinkHost(host); got != want {
			t.Fatalf("IsTunnelSinkHost(%q) = %v want %v", host, got, want)
		}
	}
}
