//go:build windows

package backend

import "fmt"

// A namespace policy prevents Windows from falling back to the physical
// adapter's corporate resolver. It applies only while the RAW tunnel is up.
// A leftover policy after a crash deliberately fails closed for system DNS;
// the next normal connection/disconnection removes our policy by its tag.
const rawDNSPolicyTag = "WDTT RAW tunnel DNS"

func rawDNSCleanupScript() string {
	return "$ErrorActionPreference='Stop'; Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '" + rawDNSPolicyTag + "' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force }; Clear-DnsClientCache"
}

func disableRawPrivateDNS() error {
	return run("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", rawDNSCleanupScript())
}

func enableRawPrivateDNS() error {
	// Force this resolver through TUN even if a previous transport installed
	// a bypass route. Do not change DNS settings on the physical adapter.
	if err := run("netsh", "interface", "ipv4", "add", "route", "1.1.1.1/32", wgIface, "store=active"); err != nil {
		return fmt.Errorf("resolver route: %w", err)
	}
	script := rawDNSCleanupScript() + "; Add-DnsClientNrptRule -Namespace '.' -NameServers '1.1.1.1' -Comment '" + rawDNSPolicyTag + "'; Clear-DnsClientCache"
	return run("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
}
