package wbxray

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildConfigJSON_Global(t *testing.T) {
	raw, err := BuildConfigJSON(Config{
		SocksPort:  19080,
		Mode:       RoutingGlobal,
		TunGateway: "10.99.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		`"gateway"`,
		`10.99.0.1/24`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in config:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"autoOutboundsInterface"`) {
		t.Fatalf("global mode without egress must omit autoOutboundsInterface:\n%s", s)
	}
	for _, bad := range []string{`"autoRoute"`, `"inet4_address"`, `"strictRoute"`, `"autoSystemRoutingTable"`} {
		if strings.Contains(s, bad) {
			t.Fatalf("legacy field %q should not appear", bad)
		}
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	routing := doc["routing"].(map[string]interface{})
	rules := routing["rules"].([]interface{})
	if len(rules) < 2 {
		t.Fatalf("expected signaling + default rules, got %d", len(rules))
	}
	last := rules[len(rules)-1].(map[string]interface{})
	if last["outboundTag"] != "proxy" {
		t.Fatalf("default outbound: %v", last["outboundTag"])
	}
	if !hasLANDirectRule(rules) {
		t.Fatalf("global mode must always direct LAN/sink before catch-all:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"port": 19080`) {
		t.Fatalf("socks port missing: %s", raw)
	}
}

func TestBuildConfigJSON_BypassLAN(t *testing.T) {
	raw, err := BuildConfigJSON(Config{Mode: RoutingBypassLAN, SocksPort: 1080})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "192.168.0.0/16") {
		t.Fatalf("LAN CIDR missing")
	}
}

func TestBuildConfigJSON_GlobalAlwaysLANDirect(t *testing.T) {
	raw, err := BuildConfigJSON(Config{Mode: RoutingGlobal, SocksPort: 1080})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	rules := doc["routing"].(map[string]interface{})["rules"].([]interface{})
	if !hasLANDirectRule(rules) {
		t.Fatalf("expected always-on LAN/sink direct rule:\n%s", raw)
	}
	for _, want := range []string{"192.168.0.0/16", "172.31.255.254", "10.99.0.0/24"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %q in global config:\n%s", want, raw)
		}
	}
	if !hasQUICBlockInDoc(doc) {
		t.Fatalf("global must always block QUIC UDP/443:\n%s", raw)
	}
}

func TestBuildConfigJSON_CustomAlwaysBlocksQUIC(t *testing.T) {
	raw, err := BuildConfigJSON(Config{Mode: RoutingCustom, SocksPort: 1080, CustomRulesJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if !hasQUICBlockInDoc(doc) {
		t.Fatalf("empty custom must still block QUIC:\n%s", raw)
	}
	outs := doc["outbounds"].([]interface{})
	haveBlock := false
	for _, o := range outs {
		if o.(map[string]interface{})["tag"] == "block" {
			haveBlock = true
		}
	}
	if !haveBlock {
		t.Fatalf("block outbound missing:\n%s", raw)
	}
}

func hasQUICBlockInDoc(doc map[string]interface{}) bool {
	rules := doc["routing"].(map[string]interface{})["rules"].([]interface{})
	return hasQUICBlockRule(rules)
}

func hasLANDirectRule(rules []interface{}) bool {
	for _, r := range rules {
		m, ok := r.(map[string]interface{})
		if !ok || m["outboundTag"] != "direct" {
			continue
		}
		ips, ok := m["ip"].([]interface{})
		if !ok {
			continue
		}
		haveLAN, haveSink := false, false
		for _, ip := range ips {
			s, _ := ip.(string)
			if s == "192.168.0.0/16" {
				haveLAN = true
			}
			if s == "172.31.255.254" {
				haveSink = true
			}
		}
		if haveLAN && haveSink {
			return true
		}
	}
	return false
}

func TestBuildConfigJSON_DirectEgress(t *testing.T) {
	raw, err := BuildConfigJSON(Config{
		Mode:              RoutingRUDirect,
		SocksPort:         1080,
		EgressInterface:   "Ethernet",
		EgressIfIndex:     4,
		EgressSendThrough: "192.168.1.42",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		`"sendThrough": "192.168.1.42"`,
		`"interface": "Ethernet"`,
		`"autoOutboundsInterface": "Ethernet"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in config:\n%s", want, s)
		}
	}
}

func TestBuildConfigJSON_CustomGeoIPRUBeforeCatchAll(t *testing.T) {
	raw, err := BuildConfigJSON(Config{
		Mode:            RoutingCustom,
		SocksPort:       1080,
		CustomRulesJSON: `[{"type":"field","outboundTag":"direct","ip":["geoip:ru"],"domain":["geosite:category-gov-ru"]}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"domainStrategy": "IPIfNonMatch"`) {
		t.Fatalf("expected IPIfNonMatch for custom split routing:\n%s", s)
	}
	if !strings.Contains(s, `"domainMatcher": "hybrid"`) {
		t.Fatalf("expected domainMatcher hybrid:\n%s", s)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	rules := doc["routing"].(map[string]interface{})["rules"].([]interface{})
	ruIdx, catchAllIdx := -1, -1
	for i, r := range rules {
		m := r.(map[string]interface{})
		if ips, ok := m["ip"].([]interface{}); ok {
			for _, ip := range ips {
				if ip == "geoip:ru" && m["outboundTag"] == "direct" {
					ruIdx = i
				}
			}
		}
		if m["network"] == "tcp,udp" && m["outboundTag"] == "proxy" {
			catchAllIdx = i
		}
	}
	if ruIdx < 0 || catchAllIdx < 0 {
		t.Fatalf("missing geoip:ru direct (%d) or catch-all proxy (%d) rule", ruIdx, catchAllIdx)
	}
	if ruIdx >= catchAllIdx {
		t.Fatalf("geoip:ru rule (idx %d) must precede catch-all proxy (idx %d)", ruIdx, catchAllIdx)
	}
}

func TestBuildConfigJSON_CustomInvalid(t *testing.T) {
	_, err := BuildConfigJSON(Config{Mode: RoutingCustom, CustomRulesJSON: "not-json"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildConfigJSON_DirectEgressNonASCIIInterface(t *testing.T) {
	raw, err := BuildConfigJSON(Config{
		Mode:              RoutingGlobal,
		SocksPort:         1080,
		EgressInterface:   "Беспроводная сеть",
		EgressIfIndex:     6,
		EgressSendThrough: "192.168.1.42",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"sendThrough": "192.168.1.42"`) {
		t.Fatalf("missing sendThrough:\n%s", s)
	}
	if !strings.Contains(s, `"interface": "6"`) {
		t.Fatalf("expected ifIndex bind for non-ASCII interface:\n%s", s)
	}
	if !strings.Contains(s, `"autoOutboundsInterface": "6"`) {
		t.Fatalf("expected ifIndex autoOutboundsInterface:\n%s", s)
	}
	if strings.Contains(s, `"interface": "Беспроводная сеть"`) {
		t.Fatalf("non-ASCII interface must not be bound:\n%s", s)
	}
}

func TestConfig_tunGatewayCIDR(t *testing.T) {
	got := Config{TunIP: "10.99.0.2", TunPrefix: 24}.tunGatewayCIDR()
	if got != "10.99.0.1/24" {
		t.Fatalf("derived gateway: got %q", got)
	}
	got = Config{TunGateway: "10.88.0.1", TunPrefix: 16}.tunGatewayCIDR()
	if got != "10.88.0.1/16" {
		t.Fatalf("explicit gateway: got %q", got)
	}
}
