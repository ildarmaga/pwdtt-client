//go:build windows

package desktoptun

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// TunAdapterIfIndex resolves the xray/wintun TUN adapter's InterfaceIndex.
// It prefers a NetAdapter named `want`, else the live Xray/Wintun netdev
// (which Windows may have given a localized alias). Returns (idx, true) when found.
func TunAdapterIfIndex(want string) (uint32, bool) {
	esc := strings.ReplaceAll(want, "'", "''")
	// Prefer exact name match; otherwise pick the live Xray/Wintun device. When
	// several match (stale ghosts), prefer Status=Up, then the highest ifIndex
	// (most recently created — the adapter the current xray just made).
	script := fmt.Sprintf(`
$byName = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue
if ($byName) { Write-Output ([string]$byName.ifIndex); exit 0 }
$all = @(Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceDescription -match 'Xray|Wintun' })
if ($all.Count -eq 0) { exit 0 }
$up = @($all | Where-Object { $_.Status -eq 'Up' } | Sort-Object ifIndex -Descending)
if ($up.Count -ge 1) { Write-Output ([string]$up[0].ifIndex); exit 0 }
$any = @($all | Sort-Object ifIndex -Descending)
Write-Output ([string]$any[0].ifIndex)
`, esc)
	out, err := runHidden("powershell", "-NoProfile", "-Command", script)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

func maskToPrefixLen(mask string) int {
	ip := net.ParseIP(mask)
	if ip == nil {
		return 24
	}
	v4 := ip.To4()
	if v4 == nil {
		return 24
	}
	ones, _ := net.IPMask(v4).Size()
	if ones == 0 {
		return 24
	}
	return ones
}

func enableAdapterIdx(idx uint32) error {
	// Enable-NetAdapter has no -InterfaceIndex parameter; pipe from Get-NetAdapter.
	_, err := runHidden("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("Get-NetAdapter -InterfaceIndex %d -ErrorAction SilentlyContinue | Enable-NetAdapter -Confirm:$false -ErrorAction SilentlyContinue", idx))
	return err
}

func setAdapterIPIdx(idx uint32, ipStr string, prefixLen int) error {
	script := fmt.Sprintf(`
Remove-NetIPAddress -InterfaceIndex %d -AddressFamily IPv4 -Confirm:$false -ErrorAction SilentlyContinue
Remove-NetRoute -InterfaceIndex %d -AddressFamily IPv4 -Confirm:$false -ErrorAction SilentlyContinue
New-NetIPAddress -InterfaceIndex %d -IPAddress '%s' -PrefixLength %d -ErrorAction Stop | Out-Null
`, idx, idx, idx, ipStr, prefixLen)
	_, err := runHidden("powershell", "-NoProfile", "-Command", script)
	return err
}

func addRouteViaIdx(prefix string, idx uint32, nexthop string, metric int) error {
	script := fmt.Sprintf(
		"New-NetRoute -DestinationPrefix '%s' -InterfaceIndex %d -NextHop '%s' -RouteMetric %d -PolicyStore ActiveStore -ErrorAction Stop | Out-Null",
		prefix, idx, nexthop, metric)
	_, err := runHidden("powershell", "-NoProfile", "-Command", script)
	return err
}

func setAdapterMetricIdx(idx uint32, metric int) error {
	_, err := runHidden("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("Set-NetIPInterface -InterfaceIndex %d -AddressFamily IPv4 -InterfaceMetric %d -ErrorAction SilentlyContinue", idx, metric))
	return err
}

func setAdapterMTUIdx(idx uint32, mtu int) error {
	_, err := runHidden("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("Set-NetIPInterface -InterfaceIndex %d -AddressFamily IPv4 -NlMtuBytes %d -ErrorAction SilentlyContinue", idx, mtu))
	return err
}

func clearAdapterDNSIdx(idx uint32) error {
	_, err := runHidden("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("Set-DnsClientServerAddress -InterfaceIndex %d -ResetServerAddresses -ErrorAction SilentlyContinue", idx))
	return err
}

// WaitAdapterUpIdx waits until the adapter (by index) is Up with a non-APIPA IPv4.
func WaitAdapterUpIdx(idx uint32, tunIP string) bool {
	script := fmt.Sprintf(`
$ip = @(Get-NetIPAddress -InterfaceIndex %d -AddressFamily IPv4 -ErrorAction SilentlyContinue |
  Where-Object { $_.IPAddress -eq '%s' })
if ($ip.Count -ge 1) { exit 0 } else { exit 1 }
`, idx, tunIP)
	_, err := runHidden("powershell", "-NoProfile", "-Command", script)
	return err == nil
}

// bestEffortRenameToWant tries to give the TUN adapter the wanted alias for UI
// consistency. Purges ghost (non-present) Xray/Wintun devices that may reserve
// the alias, then renames by ifIndex. Non-fatal — routing uses ifIndex.
func bestEffortRenameToWant(want string, idx uint32) {
	esc := strings.ReplaceAll(want, "'", "''")
	script := fmt.Sprintf(`
$want = '%s'
Get-PnpDevice -Class Net -ErrorAction SilentlyContinue | Where-Object {
  $_.Status -ne 'OK' -and ($_.FriendlyName -match 'Xray|Wintun' -or $_.FriendlyName -eq $want)
} | ForEach-Object { pnputil /remove-device $_.InstanceId 2>$null | Out-Null }
$cur = Get-NetAdapter -InterfaceIndex %d -ErrorAction SilentlyContinue
if (-not $cur) { exit 0 }
if ($cur.Name -eq $want) { exit 0 }
$stale = Get-NetAdapter -Name $want -ErrorAction SilentlyContinue
if ($stale -and $stale.ifIndex -ne %d) {
  Disable-NetAdapter -InterfaceIndex $stale.ifIndex -Confirm:$false -ErrorAction SilentlyContinue
  Remove-NetAdapter -InterfaceIndex $stale.ifIndex -Confirm:$false -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 200
}
Rename-NetAdapter -InterfaceIndex %d -NewName $want -ErrorAction SilentlyContinue
`, esc, idx, idx, idx)
	_, _ = runHidden("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
}
