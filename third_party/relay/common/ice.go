package common

import (
	"net"
	"net/url"
	"strings"
)

func FixICEURL(iceURL string) string {
	idx := strings.Index(iceURL, ":")
	if idx < 0 {
		return iceURL
	}
	scheme := iceURL[:idx]
	if scheme != "turn" && scheme != "stun" && scheme != "turns" && scheme != "stuns" {
		return iceURL
	}
	rest := iceURL[idx+1:]
	if strings.HasPrefix(rest, "[") {
		return iceURL
	}
	if strings.Count(rest, ":") <= 1 {
		return iceURL
	}
	params := ""
	if qm := strings.Index(rest, "?"); qm >= 0 {
		params = rest[qm:]
		rest = rest[:qm]
	}
	lastColon := strings.LastIndex(rest, ":")
	if lastColon > 0 {
		host := rest[:lastColon]
		port := rest[lastColon+1:]
		if net.ParseIP(host) != nil {
			return scheme + ":[" + host + "]:" + port + params
		}
	}
	if net.ParseIP(rest) != nil {
		return scheme + ":[" + rest + "]" + params
	}
	return iceURL
}

// WBBypassHosts returns hostnames whose traffic must bypass the TUN adapter
// so WebRTC signaling, SFU relay, TURN/STUN stay on the physical NIC.
func WBBypassHosts(serverURL string) []string {
	hosts := []string{
		"stream.wb.ru",
		"auth-stream.wb.ru",
		"wb-stream-turn-1.wb.ru",
		"rtc-el-01.wb.ru",
	}
	if u, err := url.Parse(strings.TrimSpace(serverURL)); err == nil {
		if h := u.Hostname(); h != "" {
			hosts = append(hosts, h)
		}
	}
	return dedupStrings(hosts)
}

// ICEHostsFromURLs collects unique non-literal hostnames from ICE server URLs.
func ICEHostsFromURLs(urls []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, u := range urls {
		h := ExtractICEHost(u)
		if h == "" || net.ParseIP(h) != nil {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

func dedupStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := ss[:0]
	for _, s := range ss {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func ExtractICEHost(iceURL string) string {
	idx := strings.Index(iceURL, ":")
	if idx < 0 {
		return ""
	}
	rest := iceURL[idx+1:]
	params := strings.Index(rest, "?")
	if params >= 0 {
		rest = rest[:params]
	}
	host, _, err := net.SplitHostPort(rest)
	if err != nil {
		return rest
	}
	return host
}
