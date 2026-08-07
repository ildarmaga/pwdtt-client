package backend

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// parseRawConfig extracts IP/DNS/MTU from qWDTT-compatible RAWCONF response:
//
//	IP = 10.70.66.5
//	DNS = 1.1.1.1
//	MTU = 1280
func parseRawConfig(conf string) (ip, dns string, mtu int, err error) {
	mtu = 1280
	scanner := bufio.NewScanner(strings.NewReader(conf))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		switch key {
		case "ip", "address":
			ip = val
		case "dns":
			dns = val
		case "mtu":
			if n, e := strconv.Atoi(val); e == nil && n >= 576 && n <= 1500 {
				mtu = n
			}
		}
	}
	if ip == "" {
		return "", "", 0, fmt.Errorf("IP not found in raw config")
	}
	// Strip CIDR if present; we always use /24 for 10.70.66.x.
	if i := strings.IndexByte(ip, '/'); i >= 0 {
		ip = ip[:i]
	}
	return ip, dns, mtu, nil
}
