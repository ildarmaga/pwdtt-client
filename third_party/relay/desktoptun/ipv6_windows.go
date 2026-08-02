//go:build windows

package desktoptun

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
)

var (
	ipv6Mu       sync.Mutex
	ipv6Disabled []string // adapters where ms_tcpip6 was disabled
)

// disableIPv6ExceptTunnel turns off the IPv6 protocol binding on every up
// adapter except the wintun tunnel. Chrome and other browsers otherwise prefer
// IPv6 default routes on Wi‑Fi/Ethernet and bypass the IPv4-only split tunnel.
// Returns adapter names where IPv6 was disabled.
func disableIPv6ExceptTunnel(tunnelAdapter string) []string {
	ipv6Mu.Lock()
	defer ipv6Mu.Unlock()
	if len(ipv6Disabled) > 0 {
		out := make([]string, len(ipv6Disabled))
		copy(out, ipv6Disabled)
		return out
	}
	psOut, err := runHidden("powershell", "-NoProfile", "-Command",
		"Get-NetAdapter -Physical -ErrorAction SilentlyContinue "+
			"| Where-Object { $_.Status -eq 'Up' -and $_.Name -ne '"+escapePS(tunnelAdapter)+"' } "+
			"| ForEach-Object { "+
			"$b = Get-NetAdapterBinding -Name $_.Name -ComponentID ms_tcpip6 -ErrorAction SilentlyContinue; "+
			"if ($b -and $b.Enabled) { "+
			"Disable-NetAdapterBinding -Name $_.Name -ComponentID ms_tcpip6 -Confirm:$false -ErrorAction Stop; "+
			"$_.Name "+
			"} "+
			"} "+
			"| ConvertTo-Json -Compress")
	if err != nil {
		return nil
	}
	ipv6Disabled = parsePSStringList(psOut)
	ret := make([]string, len(ipv6Disabled))
	copy(ret, ipv6Disabled)
	return ret
}

func restoreIPv6Bindings() {
	ipv6Mu.Lock()
	names := ipv6Disabled
	ipv6Disabled = nil
	ipv6Mu.Unlock()
	for _, name := range names {
		_, _ = runHidden("powershell", "-NoProfile", "-Command",
			"Enable-NetAdapterBinding -Name '"+escapePS(name)+"' -ComponentID ms_tcpip6 -Confirm:$false -ErrorAction SilentlyContinue")
	}
}

func parsePSStringList(out []byte) []string {
	body := bytes.TrimSpace(out)
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return nil
	}
	if body[0] == '"' {
		var one string
		if json.Unmarshal(body, &one) == nil && one != "" {
			return []string{one}
		}
		return nil
	}
	var many []string
	if json.Unmarshal(body, &many) == nil {
		return filterNonEmpty(many)
	}
	return nil
}

func filterNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func escapePS(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
