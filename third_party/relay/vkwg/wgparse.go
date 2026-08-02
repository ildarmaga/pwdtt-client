package vkwg

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
)

// parsedWG holds the pieces of a wg-quick INI config needed to bring up a
// userspace (netstack) WireGuard device.
type parsedWG struct {
	addresses []netip.Addr // [Interface] Address entries (host part)
	dns       []netip.Addr // [Interface] DNS entries
	mtu       int          // [Interface] MTU (0 => caller default)
	uapi      string       // UAPI config for device.IpcSetOperation (no set=1 prefix)
}

// parseWGConfig converts a wg-quick INI config (with [Interface]/[Peer]
// sections, base64 keys) into the pieces required for a netstack device.
func parseWGConfig(conf string) (parsedWG, error) {
	var p parsedWG
	var sb strings.Builder
	inPeer := false
	peerWritten := false
	for _, raw := range strings.Split(conf, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[Interface]" {
			inPeer = false
			continue
		}
		if line == "[Peer]" {
			if peerWritten {
				sb.WriteString("\n")
			}
			inPeer = true
			peerWritten = true
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		if !inPeer {
			switch key {
			case "address":
				for _, a := range splitList(val) {
					if ip := hostIP(a); ip.IsValid() {
						p.addresses = append(p.addresses, ip)
					}
				}
			case "dns":
				for _, a := range splitList(val) {
					if ip, err := netip.ParseAddr(strings.TrimSpace(a)); err == nil {
						p.dns = append(p.dns, ip)
					}
				}
			case "mtu":
				fmt.Sscanf(val, "%d", &p.mtu)
			case "privatekey":
				sb.WriteString("private_key=" + toHex(val) + "\n")
			case "listenport":
				sb.WriteString("listen_port=" + val + "\n")
			}
			continue
		}

		switch key {
		case "publickey":
			sb.WriteString("public_key=" + toHex(val) + "\n")
		case "presharedkey":
			sb.WriteString("preshared_key=" + toHex(val) + "\n")
		case "endpoint":
			sb.WriteString("endpoint=" + val + "\n")
		case "allowedips":
			for _, cidr := range splitList(val) {
				if c := strings.TrimSpace(cidr); c != "" {
					sb.WriteString("allowed_ip=" + c + "\n")
				}
			}
		case "persistentkeepalive":
			sb.WriteString("persistent_keepalive_interval=" + val + "\n")
		}
	}
	sb.WriteString("\n")

	if len(p.addresses) == 0 {
		return p, fmt.Errorf("no Address in wg config")
	}
	p.uapi = sb.String()
	return p, nil
}

func splitList(s string) []string { return strings.Split(s, ",") }

// hostIP extracts the host part of an "ip" or "ip/prefix" string.
func hostIP(s string) netip.Addr {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return ip
}

// toHex converts a base64-encoded WireGuard key to lowercase hex; returns the
// input unchanged if it is not valid base64 (already hex / garbage).
func toHex(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return b64
	}
	return hex.EncodeToString(raw)
}
