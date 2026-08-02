package wbxray

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
)

var lanBypassCIDRs = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16",
}

// Config drives xray-core JSON generation for WB desktop VPN.
type Config struct {
	AdapterName string
	TunIP       string // e.g. 10.99.0.2 (client-side; xray 26 uses gateway for the TUN)
	TunGateway  string // e.g. 10.99.0.1 — on-link peer; defaults to TunIP last octet - 1
	TunPrefix   int    // e.g. 24
	MTU         int
	SocksHost   string
	SocksPort   int
	Mode        RoutingMode
	// SignalingHosts are resolved to direct outbound (stream.wb.ru, ICE STUN/TURN).
	SignalingHosts []string
	// CustomRulesJSON is raw JSON array for routing.rules when Mode=custom.
	CustomRulesJSON string
	// EgressInterface is the physical NIC alias (Wi-Fi/Ethernet) for direct outbound.
	EgressInterface string
	// EgressIfIndex is the Windows InterfaceIndex for EgressInterface (bind when alias is non-ASCII).
	EgressIfIndex uint32
	// EgressSendThrough is the local IPv4 on EgressInterface (freedom sendThrough).
	EgressSendThrough string
}

func (c Config) normalized() Config {
	out := c
	if out.AdapterName == "" {
		out.AdapterName = "WDTT-WB"
	}
	if out.TunIP == "" {
		out.TunIP = "10.99.0.2"
	}
	if out.TunPrefix <= 0 {
		out.TunPrefix = 24
	}
	if out.MTU <= 0 {
		out.MTU = 1380
	}
	if out.SocksHost == "" {
		out.SocksHost = common.SocksLocalhostIP
	}
	if out.SocksPort <= 0 {
		out.SocksPort = 10808
	}
	if out.Mode == "" {
		out.Mode = RoutingGlobal
	}
	if len(out.SignalingHosts) == 0 {
		out.SignalingHosts = common.WBBypassHosts("")
	}
	if out.EgressSendThrough == "" {
		out.EgressInterface = ""
	}
	return out
}

// domainStrategyForMode picks routing DNS strategy. Split modes use IPIfNonMatch
// so geosite/sniff rules match without an extra DNS round-trip per connection;
// global keeps IPOnDemand for full-tunnel geoip matching.
func domainStrategyForMode(mode RoutingMode) string {
	if mode == RoutingGlobal {
		return "IPOnDemand"
	}
	return "IPIfNonMatch"
}

func (c Config) tunGatewayCIDR() string {
	if c.TunGateway != "" {
		if strings.Contains(c.TunGateway, "/") {
			return c.TunGateway
		}
		return fmt.Sprintf("%s/%d", c.TunGateway, c.TunPrefix)
	}
	if ip := net.ParseIP(c.TunIP); ip != nil {
		if ip4 := ip.To4(); ip4 != nil && ip4[3] > 0 {
			ip4[3]--
			return fmt.Sprintf("%s/%d", ip4.String(), c.TunPrefix)
		}
	}
	return fmt.Sprintf("10.99.0.1/%d", c.TunPrefix)
}

// BuildConfigJSON returns xray config.json bytes.
func BuildConfigJSON(cfg Config) ([]byte, error) {
	cfg = cfg.normalized()
	rules, err := buildRoutingRules(cfg)
	if err != nil {
		return nil, err
	}
	outbounds := []interface{}{
		map[string]interface{}{
			"tag":      "proxy",
			"protocol": "socks",
			"settings": map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{
						"address": cfg.SocksHost,
						"port":    cfg.SocksPort,
					},
				},
			},
		},
		buildDirectOutbound(cfg),
	}
	outbounds = append(outbounds, map[string]interface{}{
		"tag":      "block",
		"protocol": "blackhole",
		"settings": map[string]interface{}{},
	})
	tunSettings := map[string]interface{}{
		// xray-core 26.x Windows TUN: gateway + DNS on adapter; OS routes via RouteShell.
		"name":    cfg.AdapterName,
		"mtu":     cfg.MTU,
		"gateway": []string{cfg.tunGatewayCIDR()},
		"dns":     []string{"1.1.1.1", "8.8.8.8"},
	}
	if v := tunAutoOutboundsInterface(cfg); v != "" {
		tunSettings["autoOutboundsInterface"] = v
	}

	doc := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "error",
		},
		"dns": map[string]interface{}{
			"servers": []interface{}{
				"1.1.1.1",
				"8.8.8.8",
			},
		},
		"inbounds": []interface{}{
			map[string]interface{}{
				"tag":      "tun-in",
				"protocol": "tun",
				"settings": tunSettings,
				"sniffing": map[string]interface{}{
					"enabled":      true,
					"destOverride": []string{"http", "tls"},
				},
			},
		},
		"outbounds": outbounds,
		"routing": map[string]interface{}{
			// hybrid: required for geosite.dat (incl. Loyalsoldier builds).
			"domainMatcher":  "hybrid",
			"domainStrategy": domainStrategyForMode(cfg.Mode),
			"rules":          rules,
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

func buildRoutingRules(cfg Config) ([]interface{}, error) {
	var rules []interface{}

	for _, host := range cfg.SignalingHosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"domain":      []string{"full:" + host, "domain:" + host},
			"outboundTag": "direct",
		})
	}

	// Always keep LAN + tunnel-sink hosts off the SOCKS/KCP carrier, even in
	// global/custom. OS RouteShell should already bypass these; this is defense
	// in depth when a packet still reaches xray (metric race, missing OS route).
	rules = append(rules, map[string]interface{}{
		"type":        "field",
		"ip":          append(append([]string(nil), lanBypassCIDRs...), "172.31.255.254", "10.99.0.0/24"),
		"outboundTag": "direct",
	})

	switch cfg.Mode {
	case RoutingBypassLAN:
		// LAN already covered above; keep mode as a no-op extra for clarity.
	case RoutingRUDirect:
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"ip":          []string{"geoip:ru"},
			"outboundTag": "direct",
		})
	case RoutingCustom:
		custom := strings.TrimSpace(cfg.CustomRulesJSON)
		if custom == "" {
			custom = "[]"
		}
		var extra []interface{}
		if err := json.Unmarshal([]byte(custom), &extra); err != nil {
			return nil, fmt.Errorf("wbxray: custom routing rules: %w", err)
		}
		rules = append(rules, extra...)
	}

	// Always block QUIC (UDP/443) before catch-all. Empty custom mode used to
	// omit this (rules=6 = signaling+LAN+proxy only) while global preset had it;
	// Chrome/Edge QUIC then flooded the shared KCP carrier alongside Telegram.
	if !hasQUICBlockRule(rules) {
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"network":     "udp",
			"port":        "443",
			"outboundTag": "block",
		})
	}

	rules = append(rules, map[string]interface{}{
		"type":        "field",
		"network":     "tcp,udp",
		"outboundTag": "proxy",
	})
	return rules, nil
}

func hasQUICBlockRule(rules []interface{}) bool {
	for _, r := range rules {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if m["outboundTag"] != "block" {
			continue
		}
		net, _ := m["network"].(string)
		port := fmt.Sprint(m["port"])
		if strings.Contains(net, "udp") && (port == "443" || strings.Contains(port, "443")) {
			return true
		}
	}
	return false
}

func buildDirectOutbound(cfg Config) map[string]interface{} {
	out := map[string]interface{}{
		"tag":      "direct",
		"protocol": "freedom",
		"settings": map[string]interface{}{
			"domainStrategy": "UseIP",
		},
	}
	if cfg.EgressSendThrough != "" {
		out["sendThrough"] = cfg.EgressSendThrough
	}
	if iface := egressBindInterface(cfg); iface != "" {
		out["streamSettings"] = map[string]interface{}{
			"sockopt": map[string]interface{}{
				"interface": iface,
			},
		}
	}
	return out
}

func tunAutoOutboundsInterface(cfg Config) string {
	return egressBindInterface(cfg)
}

// egressBindInterface picks a bind target xray accepts on Windows: ASCII alias,
// numeric ifIndex, or local IPv4 — never "auto" (breaks on Cyrillic Wi‑Fi names).
func egressBindInterface(cfg Config) string {
	if egressInterfaceBindable(cfg.EgressInterface) {
		return cfg.EgressInterface
	}
	if cfg.EgressIfIndex > 0 {
		return fmt.Sprintf("%d", cfg.EgressIfIndex)
	}
	if cfg.EgressSendThrough != "" {
		return cfg.EgressSendThrough
	}
	return ""
}

func egressInterfaceBindable(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r > 127 {
			return false
		}
	}
	return true
}
