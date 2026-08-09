package backend

import "testing"

func TestVKTransportIncludesCallsTURN(t *testing.T) {
	want := []string{"90.156.0.0/16", "91.231.0.0/16", "95.163.0.0/16", "155.212.192.0/20"}
	got := map[string]bool{}
	for _, c := range vkTransportCIDRs {
		got[c] = true
	}
	for _, c := range want {
		if !got[c] {
			t.Fatalf("vkTransportCIDRs missing %s — TURN hairpin after tunnel up", c)
		}
	}
}
