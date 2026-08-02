package wbxray

import "strings"

// RoutingMode selects preset xray routing rules (v2rayN-style).
type RoutingMode string

const (
	RoutingGlobal    RoutingMode = "global"
	RoutingBypassLAN RoutingMode = "bypass_lan"
	RoutingRUDirect  RoutingMode = "ru_direct"
	RoutingCustom    RoutingMode = "custom"
)

func ParseRoutingMode(s string) RoutingMode {
	switch RoutingMode(strings.ToLower(strings.TrimSpace(s))) {
	case RoutingBypassLAN:
		return RoutingBypassLAN
	case RoutingRUDirect:
		return RoutingRUDirect
	case RoutingCustom:
		return RoutingCustom
	default:
		return RoutingGlobal
	}
}

func (m RoutingMode) Label() string {
	switch m {
	case RoutingBypassLAN:
		return "Bypass LAN"
	case RoutingRUDirect:
		return "RU direct"
	case RoutingCustom:
		return "Custom"
	default:
		return "Global"
	}
}
