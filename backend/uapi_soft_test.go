//go:build windows

package backend

import (
	"strings"
	"testing"
)

func TestUapiSoftReplacePeers(t *testing.T) {
	wgConf := `[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEE=
Address = 10.66.66.2/32

[Peer]
PublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBEE=
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
`
	uapi := "replace_peers=true\n" + uapiConf(wgConf)
	if !strings.HasPrefix(uapi, "replace_peers=true\n") {
		t.Fatal("must start with replace_peers")
	}
	if !strings.Contains(uapi, "private_key=") {
		t.Fatal("missing private_key")
	}
	if !strings.Contains(uapi, "public_key=") {
		t.Fatal("missing public_key")
	}
	if !strings.Contains(uapi, "allowed_ip=0.0.0.0/0") {
		t.Fatal("missing allowed_ip")
	}
}
