package desktoptun

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

type psEgressRaw struct {
	NextHop         string          `json:"NextHop"`
	InterfaceAlias  string          `json:"InterfaceAlias"`
	InterfaceIndex  json.RawMessage `json:"InterfaceIndex"`
	LocalIP         json.RawMessage `json:"LocalIP"`
}

// parseEgressRouteJSON decodes PowerShell ConvertTo-Json output for default route egress.
func parseEgressRouteJSON(body []byte) (gateway, alias, localIP string, ifIndex uint32, err error) {
	body = trimBOM(body)
	if len(body) == 0 {
		return "", "", "", 0, fmt.Errorf("empty egress json")
	}
	var row psEgressRaw
	if err := json.Unmarshal(body, &row); err != nil {
		return "", "", "", 0, fmt.Errorf("parse egress route: %w", err)
	}
	if row.NextHop == "" || net.ParseIP(row.NextHop) == nil {
		return "", "", "", 0, fmt.Errorf("no usable default route (out=%s)", string(body))
	}
	localIP = parsePSJSONString(row.LocalIP)
	if localIP == "" || net.ParseIP(localIP) == nil {
		if ip := fallbackLocalIPv4(row.NextHop); ip != "" {
			localIP = ip
		}
	}
	ifIndex = parsePSJSONUint32(row.InterfaceIndex)
	return row.NextHop, row.InterfaceAlias, localIP, ifIndex, nil
}

func parsePSJSONUint32(raw json.RawMessage) uint32 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var n uint32
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f > 0 {
		return uint32(f)
	}
	s := strings.TrimSpace(parsePSJSONString(raw))
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

func parsePSJSONString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var ss []string
	if err := json.Unmarshal(raw, &ss); err == nil {
		for _, v := range ss {
			v = strings.TrimSpace(v)
			if v != "" {
				return v
			}
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, key := range []string{"IPAddress", "Value", "value", "Address"} {
			if v, ok := obj[key]; ok {
				if s := parsePSJSONString(v); s != "" {
					return s
				}
			}
		}
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

// fallbackLocalIPv4 picks a host IPv4 on the same subnet as the gateway, else any LAN IP.
func fallbackLocalIPv4(gateway string) string {
	gw := net.ParseIP(gateway)
	if gw == nil {
		return ""
	}
	gw4 := gw.To4()
	if gw4 == nil {
		return ""
	}
	var any string
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || (ip4[0] == 169 && ip4[1] == 254) {
				continue
			}
			if ipnet.Contains(gw4) {
				return ip4.String()
			}
			if any == "" && (ip4[0] == 10 || ip4[0] == 172 || ip4[0] == 192) {
				any = ip4.String()
			}
		}
	}
	return any
}

func trimBOM(b []byte) []byte {
	return bytesTrimSpace(b)
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
