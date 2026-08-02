//go:build windows

package desktoptun

import "sync"

var (
	physicalEgressMu sync.RWMutex
	cachedPhysGW     string
	cachedPhysIP     string
)

func rememberPhysicalEgress(gateway, localIP string) {
	physicalEgressMu.Lock()
	cachedPhysGW = gateway
	cachedPhysIP = localIP
	physicalEgressMu.Unlock()
}

// RememberPhysicalEgress stores the LAN gateway captured before VPN split routes
// so updater / GitHub bypass can egress direct even while VK TUN is up.
func RememberPhysicalEgress(gateway, localIP string) {
	rememberPhysicalEgress(gateway, localIP)
}

func forgetPhysicalEgress() {
	physicalEgressMu.Lock()
	cachedPhysGW = ""
	cachedPhysIP = ""
	physicalEgressMu.Unlock()
}

// physicalIPv4Egress returns the pre-VPN LAN gateway for direct egress (updates,
// GitHub bypass). Falls back to querying a non-TUN default route.
func physicalIPv4Egress() (gateway, localIP string, err error) {
	physicalEgressMu.RLock()
	gw, ip := cachedPhysGW, cachedPhysIP
	physicalEgressMu.RUnlock()
	if gw != "" {
		return gw, ip, nil
	}
	return physicalIPv4EgressQuery()
}

// physicalIPv4EgressQuery picks the best default route that is not the WDTT TUN.
func physicalIPv4EgressQuery() (gateway, localIP string, err error) {
	gw, _, ip, _, err := physicalIPv4EgressFull()
	return gw, ip, err
}

func physicalIPv4EgressFull() (gateway, alias, localIP string, ifIndex uint32, err error) {
	out, err := runHidden("powershell", "-NoProfile", "-Command",
		`$r = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -AddressFamily IPv4 -ErrorAction Stop |
  Where-Object { $_.InterfaceAlias -notlike 'WDTT-WB-*' -and $_.NextHop -ne '10.99.0.1' } |
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
