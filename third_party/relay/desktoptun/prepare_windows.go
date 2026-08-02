//go:build windows

package desktoptun

import (
	"fmt"
	"strings"
	"time"
)

// TeardownTunAdapter drops routes and removes the wintun adapter (call after xray exits).
func TeardownTunAdapter(adapterName string) {
	if adapterName == "" {
		return
	}
	EmergencyDown(adapterName)
	deepClearWintun(adapterName)
}

// PrepareBeforeStart clears stale split routes and disables a ghost wintun
// adapter left after a crashed session so CreateAdapter can succeed.
func PrepareBeforeStart(adapterName string) {
	if adapterName == "" {
		return
	}
	EmergencyDown(adapterName)
	removeStaleAdapter(adapterName)
	time.Sleep(200 * time.Millisecond)
}

// DeepPrepareBeforeStart full ghost-adapter cleanup for xray retry after exit 23.
func DeepPrepareBeforeStart(adapterName string) {
	if adapterName == "" {
		return
	}
	EmergencyDown(adapterName)
	deepClearWintun(adapterName)
}

// CleanupAllWDTTAdapters removes WDTT-WB and legacy WDTT-WB-* adapters.
func CleanupAllWDTTAdapters() {
	deepCleanupLegacyAdapters()
}

func quickClearWintun(adapterName string) {
	_ = QuickReleaseWintunPool(adapterName)
	quickCleanupLegacyAdapters()
	removeStaleAdapter(adapterName)
	time.Sleep(150 * time.Millisecond)
}

func deepClearWintun(adapterName string) {
	_ = ForceReleaseWintunPool(adapterName)
	deepCleanupLegacyAdapters()
	removeStaleAdapter(adapterName)
	_ = ForceReleaseWintunPool(adapterName)
	for _, legacy := range listLegacyWintunPoolNames() {
		_ = ReleaseWintunPool(legacy)
	}
	deepCleanupLegacyAdapters()
	time.Sleep(300 * time.Millisecond)
}

func quickCleanupLegacyAdapters() {
	script := `
$targets = @(Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object {
  $_.Name -like 'WDTT-WB*' -or $_.InterfaceDescription -match 'Wintun|Xray'
})
foreach ($a in $targets) {
  Disable-NetAdapter -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue
  Remove-NetAdapter -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue
}
`
	_, _ = runHidden("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
}

func deepCleanupLegacyAdapters() {
	script := `
$targets = @(Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object {
  $_.Name -like 'WDTT-WB*' -or $_.InterfaceDescription -match 'Wintun|Xray'
})
foreach ($a in $targets) {
  Disable-NetAdapter -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue
}
Start-Sleep -Milliseconds 400
foreach ($a in $targets) {
  Remove-NetAdapter -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue
}
Start-Sleep -Milliseconds 300
Get-PnpDevice -Class Net -ErrorAction SilentlyContinue | Where-Object {
  ($_.FriendlyName -eq 'Xray Tunnel') -or ($_.FriendlyName -match 'Wintun Tunnel|WDTT-WB')
} | ForEach-Object {
  pnputil /remove-device $_.InstanceId 2>$null | Out-Null
}
`
	_, _ = runHidden("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	time.Sleep(200 * time.Millisecond)
}

// EnsureAdapterAbsent retries removal until the named adapter is gone or attempts exhaust.
func EnsureAdapterAbsent(adapterName string) error {
	if adapterName == "" {
		return nil
	}
	for i := 0; i < 2; i++ {
		removeStaleAdapter(adapterName)
		if !AdapterPresent(adapterName) && !xrayTunnelNetdevPresent() {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	deepCleanupLegacyAdapters()
	time.Sleep(400 * time.Millisecond)
	if AdapterPresent(adapterName) {
		return fmt.Errorf("adapter %q still present after cleanup", adapterName)
	}
	if xrayTunnelNetdevPresent() {
		return fmt.Errorf("xray tunnel netdev still present after cleanup")
	}
	return nil
}

// AdapterPresent reports whether a NetAdapter with the given name exists.
func AdapterPresent(name string) bool {
	return adapterPresent(name)
}

func xrayTunnelNetdevPresent() bool {
	out, err := runHidden("powershell", "-NoProfile", "-Command",
		`(Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceDescription -match 'Wintun|Xray' }).Count -gt 0`)
	return err == nil && strings.TrimSpace(string(out)) == "True"
}

func adapterPresent(name string) bool {
	esc := strings.ReplaceAll(name, "'", "''")
	out, err := runHidden("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("(Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue) -ne $null", esc))
	return err == nil && strings.TrimSpace(string(out)) == "True"
}

// removeStaleAdapter disables and removes one leftover adapter.
func removeStaleAdapter(name string) {
	if name == "" {
		return
	}
	esc := strings.ReplaceAll(name, "'", "''")
	script := fmt.Sprintf(`
$a = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue
if (-not $a) { exit 0 }
Disable-NetAdapter -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue
Remove-NetAdapter -InterfaceIndex $a.ifIndex -Confirm:$false -ErrorAction SilentlyContinue
`, esc)
	_, _ = runHidden("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
}
