package desktoptun

import "testing"

func TestParseEgressRouteJSON_StringLocalIP(t *testing.T) {
	gw, alias, ip, idx, err := parseEgressRouteJSON([]byte(
		`{"NextHop":"192.168.8.1","InterfaceAlias":"Ethernet","InterfaceIndex":4,"LocalIP":"192.168.1.203"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if gw != "192.168.8.1" || alias != "Ethernet" || ip != "192.168.1.203" || idx != 4 {
		t.Fatalf("got gw=%q alias=%q ip=%q idx=%d", gw, alias, ip, idx)
	}
}

func TestParseEgressRouteJSON_ObjectLocalIP(t *testing.T) {
	gw, alias, ip, _, err := parseEgressRouteJSON([]byte(
		`{"NextHop":"192.168.8.1","InterfaceAlias":"Ethernet","InterfaceIndex":7,"LocalIP":{"IPAddress":"192.168.1.203","PrefixOrigin":4}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if ip != "192.168.1.203" {
		t.Fatalf("ip=%q", ip)
	}
	if gw != "192.168.8.1" || alias != "Ethernet" {
		t.Fatalf("gw=%q alias=%q", gw, alias)
	}
}

func TestParseEgressRouteJSON_ArrayLocalIP(t *testing.T) {
	_, _, ip, _, err := parseEgressRouteJSON([]byte(
		`{"NextHop":"10.0.0.1","InterfaceAlias":"Wi-Fi","InterfaceIndex":12,"LocalIP":["10.0.0.42"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if ip != "10.0.0.42" {
		t.Fatalf("ip=%q", ip)
	}
}

func TestParseEgressRouteJSON_EmptyLocalIPUsesFallback(t *testing.T) {
	// fallbackLocalIPv4 uses net.Interfaces — may find nothing in CI; just ensure parse succeeds.
	gw, _, _, _, err := parseEgressRouteJSON([]byte(
		`{"NextHop":"192.168.8.1","InterfaceAlias":"Ethernet","InterfaceIndex":3,"LocalIP":null}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if gw != "192.168.8.1" {
		t.Fatalf("gw=%q", gw)
	}
}
