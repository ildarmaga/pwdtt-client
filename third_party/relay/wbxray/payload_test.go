package wbxray

import (
	"strings"
	"testing"
)

func TestParseConnectPayload_CustomRules(t *testing.T) {
	raw := `{"mode":"global","xrayRules":[{"type":"field","outboundTag":"direct","ip":["geoip:ru"]}]}`
	mode, rules, err := ParseConnectPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if mode != RoutingCustom {
		t.Fatalf("mode: %v", mode)
	}
	if !strings.Contains(rules, "geoip:ru") {
		t.Fatalf("rules: %s", rules)
	}
}

func TestBuildConfigJSON_BlockOutbound(t *testing.T) {
	raw, err := BuildConfigJSON(Config{
		Mode: RoutingCustom,
		CustomRulesJSON: `[{"type":"field","outboundTag":"block","network":"udp","port":"443"}]`,
		SocksPort: 1080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"blackhole"`) {
		t.Fatalf("missing blackhole outbound: %s", raw)
	}
}
