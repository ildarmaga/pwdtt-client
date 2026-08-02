//go:build windows

package desktoptun

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// runHidden invokes a command with no console window. All desktoptun
// helpers use netsh / route / powershell here; subprocess parsing
// avoids pulling in winipcfg-style cgo or large Go bindings.
func runHidden(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	hideConsole(cmd)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s %s: %w (%s)",
			name, strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

func enableAdapter(adapter string) error {
	esc := strings.ReplaceAll(adapter, "'", "''")
	_, err := runHidden("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("Enable-NetAdapter -Name '%s' -Confirm:$false -ErrorAction Stop", esc))
	return err
}

// setAdapterIP gives the wintun adapter its tunnel IP.
func setAdapterIP(adapter, ipStr, mask string) error {
	_, err := runHidden("netsh", "interface", "ipv4", "set", "address",
		"name="+adapter, "static", ipStr, mask)
	return err
}

// setAdapterMTU sets the wintun adapter's MTU. netsh accepts this on
// any IPv4-capable interface, including wintun.
func setAdapterMTU(adapter string, mtu int) error {
	_, err := runHidden("netsh", "interface", "ipv4", "set", "subinterface",
		adapter, "mtu="+strconv.Itoa(mtu), "store=active")
	return err
}

// setAdapterDNS pins resolvers onto the wintun adapter. Windows picks
// resolvers per interface based on the routing decision, so this is
// what makes DNS lookups from non-bypassed apps go through the tunnel.
func setAdapterDNS(adapter string, servers []string) error {
	if len(servers) == 0 {
		return nil
	}
	if _, err := runHidden("netsh", "interface", "ipv4", "set", "dnsservers",
		"name="+adapter, "static", servers[0], "primary", "validate=no"); err != nil {
		return err
	}
	for _, s := range servers[1:] {
		if _, err := runHidden("netsh", "interface", "ipv4", "add", "dnsservers",
			"name="+adapter, s, "validate=no"); err != nil {
			return err
		}
	}
	return nil
}

// clearAdapterDNS removes static resolvers from the tunnel adapter so Windows
// falls back to router DNS on the physical NIC (VK-style).
func clearAdapterDNS(adapter string) error {
	_, err := runHidden("netsh", "interface", "ipv4", "set", "dnsservers",
		"name="+adapter, "source=dhcp")
	return err
}

// setAdapterMetric lowers the tunnel adapter metric so Windows prefers it for
// default-route and DNS interface selection over Wi‑Fi/Ethernet.
func setAdapterMetric(adapter string, metric int) error {
	_, err := runHidden("powershell", "-NoProfile", "-Command",
		"Set-NetIPInterface -InterfaceAlias '"+strings.ReplaceAll(adapter, "'", "''")+
			"' -AddressFamily IPv4 -InterfaceMetric "+strconv.Itoa(metric)+" -ErrorAction Stop")
	return err
}

// addRouteViaAdapter installs a prefix route bound to the wintun
// adapter. The nexthop must be on-link (typically TunnelPeer).
func addRouteViaAdapter(prefix, adapter, nexthop string, metric int) error {
	_, err := runHidden("netsh", "interface", "ipv4", "add", "route",
		"prefix="+prefix, "interface="+adapter,
		"nexthop="+nexthop, "metric="+strconv.Itoa(metric),
		"store=active")
	return err
}

func deleteRouteByPrefix(prefix, adapter string) error {
	_, err := runHidden("netsh", "interface", "ipv4", "delete", "route",
		"prefix="+prefix, "interface="+adapter)
	return err
}

// addHostRoute installs a /32 route for ip via the given gateway. Used
// for the joiner's signaling + SFU bypasses.
//
// Session-only (no -p): persistent route ADD can take seconds per entry on
// Windows (AV/WFP), leaving a hairpin window after split-default is up.
func addHostRoute(ip, gateway string, metric int) error {
	_, err := runHidden("route", "ADD", ip,
		"MASK", "255.255.255.255", gateway,
		"METRIC", strconv.Itoa(metric))
	return err
}

func deleteHostRoute(ip string) error {
	_, err := runHidden("route", "DELETE", ip)
	return err
}

// addBypassCIDR installs a prefix route via the original default gateway so the
// whole range skips the tunnel. Metric 1 beats the metric-2 split-default routes.
// Session-only (no -p) — see addHostRoute.
func addBypassCIDR(cidr, gateway string, metric int) error {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	network := ipnet.IP.String()
	mask := net.IP(ipnet.Mask).String()
	_, err = runHidden("route", "ADD", network,
		"MASK", mask, gateway, "METRIC", strconv.Itoa(metric))
	return err
}

func deleteBypassCIDR(cidr string) error {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	_, err = runHidden("route", "DELETE", ipnet.IP.String())
	return err
}

// adapterIPv4Index returns the InterfaceIndex of the wintun adapter
// once netsh has assigned an IP to it. Best-effort; used only for logs.
func adapterIPv4Index(adapter string) (uint32, error) {
	out, err := runHidden("powershell", "-NoProfile", "-Command",
		"(Get-NetAdapter -Name '"+adapter+"' -ErrorAction Stop).ifIndex")
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

// defaultIPv4Gateway picks the active default route gateway and interface alias.
func defaultIPv4Gateway() (gateway, alias string, err error) {
	gateway, alias, _, _, err = defaultIPv4Egress()
	return gateway, alias, err
}

// defaultIPv4Egress returns default-route gateway, interface alias, local IPv4, and ifIndex.
func defaultIPv4Egress() (gateway, alias, localIP string, ifIndex uint32, err error) {
	out, err := runHidden("powershell", "-NoProfile", "-Command",
		`$r = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -AddressFamily IPv4 -ErrorAction Stop |
  Sort-Object RouteMetric |
  Select-Object -First 1
if (-not $r) { exit 1 }
$ipLine = ''
$addr = Get-NetIPAddress -InterfaceIndex $r.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
  Where-Object { $_.IPAddress -notlike '169.254.*' -and $_.PrefixOrigin -ne 'WellKnown' } |
  Select-Object -First 1
if ($addr) { $ipLine = [string]$addr.IPAddress }
@{ NextHop=$r.NextHop; InterfaceAlias=$r.InterfaceAlias; InterfaceIndex=$r.InterfaceIndex; LocalIP=$ipLine } | ConvertTo-Json -Compress`)
	if err != nil {
		return "", "", "", 0, err
	}
	return parseEgressRouteJSON(out)
}
