package wbxray

import (
	"encoding/json"
	"strings"
)

// ConnectPayload is sent from the desktop UI routing editor.
type ConnectPayload struct {
	Mode      RoutingMode     `json:"mode"`
	XrayRules json.RawMessage `json:"xrayRules"`
}

// ParseConnectPayload decodes UI routing JSON. Empty input → global preset.
func ParseConnectPayload(raw string) (RoutingMode, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RoutingGlobal, "", nil
	}
	var p ConnectPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return RoutingGlobal, "", err
	}
	mode := p.Mode
	if mode == "" {
		mode = RoutingGlobal
	}
	custom := strings.TrimSpace(string(p.XrayRules))
	if custom != "" && custom != "null" && custom != "[]" {
		return RoutingCustom, custom, nil
	}
	return ParseRoutingMode(string(mode)), "", nil
}

func needsBlockOutbound(rulesJSON string) bool {
	custom := strings.TrimSpace(rulesJSON)
	if custom == "" {
		return false
	}
	var rules []map[string]interface{}
	if err := json.Unmarshal([]byte(custom), &rules); err != nil {
		return false
	}
	for _, r := range rules {
		if tag, _ := r["outboundTag"].(string); tag == "block" {
			return true
		}
	}
	return false
}
