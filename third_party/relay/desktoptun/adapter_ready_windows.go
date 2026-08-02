//go:build windows

package desktoptun

import (
	"fmt"
	"strings"
	"time"
)

// WintunAdapterSnapshot returns JSON of visible Wintun/Xray/WDTT adapters (for logs).
func WintunAdapterSnapshot() string {
	out, err := runHidden("powershell", "-NoProfile", "-Command",
		"Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceDescription -match 'Wintun|Xray|WDTT' -or $_.Name -like 'WDTT*' } | Select-Object Name,Status,InterfaceDescription | ConvertTo-Json -Compress")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// EnsureTunAdapterReady reports whether the xray/wintun TUN device exists —
// either as a NetAdapter named `want` or (more commonly on localized Windows)
// as a live device whose InterfaceDescription matches Xray/Wintun. The actual
// IP/route configuration is done by ifIndex in RouteShell.FinishTunSetup, so
// the localized alias does not matter for readiness.
func EnsureTunAdapterReady(want string) bool {
	_, ok := TunAdapterIfIndex(want)
	return ok
}

// WaitAdapterPresent blocks until the named adapter appears (any link state).
func WaitAdapterPresent(adapter string, timeout time.Duration) error {
	if adapter == "" {
		return fmt.Errorf("desktoptun: adapter name required")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if adapterPresent(adapter) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	if snap := WintunAdapterSnapshot(); snap != "" {
		return fmt.Errorf("adapter %q not present within %s (visible: %s)", adapter, timeout, snap)
	}
	return fmt.Errorf("adapter %q not present within %s", adapter, timeout)
}
func WaitAdapterUp(adapter string, timeout time.Duration) error {
	if adapter == "" {
		return fmt.Errorf("desktoptun: adapter name required")
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	esc := strings.ReplaceAll(adapter, "'", "''")
	check := fmt.Sprintf(`
$a = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue
if (-not ($a -and $a.Status -eq 'Up')) { exit 1 }
$ip = @(Get-NetIPAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
  Where-Object { $_.IPAddress -notlike '169.254*' })
if ($ip.Count -ge 1) { exit 0 } else { exit 1 }
`, esc)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := runHidden("powershell", "-NoProfile", "-Command", check); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("adapter %q not up within %s", adapter, timeout)
}

// WaitTunRoutingReady blocks until split-default or full default routes exist on the adapter.
func WaitTunRoutingReady(adapter string, timeout time.Duration) error {
	if adapter == "" {
		return fmt.Errorf("desktoptun: adapter name required")
	}
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	esc := strings.ReplaceAll(adapter, "'", "''")
	check := fmt.Sprintf(`
$a = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue
if (-not ($a -and $a.Status -eq 'Up')) { exit 1 }
$idx = $a.ifIndex
$r = @(Get-NetRoute -AddressFamily IPv4 -InterfaceIndex $idx -ErrorAction SilentlyContinue)
$split = @($r | Where-Object { $_.DestinationPrefix -in @('0.0.0.0/1','128.0.0.0/1') })
$full = @($r | Where-Object { $_.DestinationPrefix -eq '0.0.0.0/0' })
if ($split.Count -ge 2 -or $full.Count -ge 1) { exit 0 } else { exit 1 }
`, esc)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := runHidden("powershell", "-NoProfile", "-Command", check); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("tun routing not ready on %q within %s", adapter, timeout)
}
