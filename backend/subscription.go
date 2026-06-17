package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type SubTrafficStats struct {
	Upload         int64  `json:"upload"`
	Download       int64  `json:"download"`
	Total          int64  `json:"total"`
	Expire         int64  `json:"expire"`
	Title          string `json:"title,omitempty"`
	Announce       string `json:"announce,omitempty"`
	SupportURL     string `json:"supportUrl,omitempty"`
	UpdateInterval int    `json:"updateInterval,omitempty"`
}

type SubImportResult struct {
	IP       string           `json:"ip"`
	DtlsPort string           `json:"dtlsPort"`
	Password string           `json:"password"`
	Name     string           `json:"name"`
	VpnName  string           `json:"vpnName,omitempty"`
	Hashes   []string         `json:"hashes"`
	SubURL   string           `json:"subUrl"`
	DeviceID string           `json:"deviceId,omitempty"`
	Stats    *SubTrafficStats `json:"stats,omitempty"`
}

func (a *App) FetchSubscriptionURL(rawURL string) (*SubImportResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := validatePanelSubURL(rawURL); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "WDTT/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("subscription HTTP %d", resp.StatusCode)
	}

	stats := parseSubResponseStats(resp.Header)
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	link := decodeSubBody(string(bodyBytes))
	parsed, err := parseWdttLink(link)
	if err != nil {
		return nil, err
	}
	name := parsed.Name
	vpnName := parsed.VpnName
	if stats != nil {
		if vpnName == "" && stats.Title != "" {
			vpnName = stats.Title
		}
	}
	return &SubImportResult{
		IP:       parsed.IP,
		DtlsPort: parsed.DtlsPort,
		Password: parsed.Password,
		Name:     name,
		VpnName:  vpnName,
		Hashes:   parsed.Hashes,
		SubURL:   strings.Split(rawURL, "?")[0],
		DeviceID: parsed.DeviceID,
		Stats:    stats,
	}, nil
}

func (a *App) FetchSubscriptionStats(rawURL string) (*SubTrafficStats, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := validatePanelSubURL(rawURL); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "WDTT/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		req, err = http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "text/plain")
		req.Header.Set("User-Agent", "WDTT/1.0")
		resp, err = client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("subscription HTTP %d", resp.StatusCode)
		}
	}
	stats := parseSubResponseStats(resp.Header)
	if stats == nil {
		return nil, fmt.Errorf("no subscription info")
	}
	return stats, nil
}

func (a *App) ParseWdttLink(link string) (*SubImportResult, error) {
	return nil, fmt.Errorf("прямой импорт wdtt:// отключён — используйте ссылку подписки панели")
}

func parseSubResponseStats(h http.Header) *SubTrafficStats {
	stats := parseSubUserInfo(h.Get("Subscription-Userinfo"))
	if stats == nil {
		stats = &SubTrafficStats{}
	}
	applySubMeta(stats, h)
	if stats.Upload == 0 && stats.Download == 0 && stats.Total == 0 && stats.Expire == 0 &&
		stats.Title == "" && stats.Announce == "" && stats.SupportURL == "" && stats.UpdateInterval == 0 {
		return nil
	}
	return stats
}

func applySubMeta(stats *SubTrafficStats, h http.Header) {
	if stats == nil {
		return
	}
	if title := decodeSubHeaderValue(h.Get("Profile-Title")); title != "" {
		stats.Title = title
	}
	if announce := decodeSubHeaderValue(h.Get("Announce")); announce != "" {
		stats.Announce = announce
	}
	if support := strings.TrimSpace(h.Get("Profile-Web-Page-Url")); support != "" {
		stats.SupportURL = support
	}
	if iv := strings.TrimSpace(h.Get("Profile-Update-Interval")); iv != "" {
		if n, err := strconv.Atoi(iv); err == nil && n > 0 {
			stats.UpdateInterval = n
		}
	}
}

func decodeSubHeaderValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "base64:") {
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v[7:])); err == nil {
			return strings.TrimSpace(string(dec))
		}
		return ""
	}
	return v
}

func parseSubUserInfo(header string) *SubTrafficStats {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	out := &SubTrafficStats{}
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		idx := strings.Index(part, "=")
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(part[:idx]))
		val, err := strconv.ParseInt(strings.TrimSpace(part[idx+1:]), 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "upload":
			out.Upload = val
		case "download":
			out.Download = val
		case "total":
			out.Total = val
		case "expire":
			out.Expire = val
		}
	}
	return out
}

type wdttLinkParsed struct {
	IP       string
	DtlsPort string
	Password string
	Name     string
	VpnName  string
	Hashes   []string
	SubURL   string
	DeviceID string
}

func decodeSubBody(body string) string {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "wdtt://") {
		return body
	}
	if dec, err := base64.StdEncoding.DecodeString(body); err == nil {
		s := strings.TrimSpace(string(dec))
		if strings.HasPrefix(s, "wdtt://") {
			return s
		}
	}
	return body
}

func parseWdttLink(link string) (wdttLinkParsed, error) {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "wdtt://") {
		return wdttLinkParsed{}, fmt.Errorf("invalid wdtt link prefix")
	}
	payload := strings.TrimPrefix(link, "wdtt://")
	name := "Server"
	if hash := strings.Index(payload, "#"); hash >= 0 {
		if n := strings.TrimSpace(payload[hash+1:]); n != "" {
			name = n
		}
		payload = payload[:hash]
	}
	if looksColonWdtt(payload) {
		return parseColonWdtt(payload, name)
	}
	if p, err := parseJSONWdtt(payload); err == nil {
		return p, nil
	}
	return parseColonWdtt(payload, name)
}

func looksColonWdtt(payload string) bool {
	parts := strings.Split(payload, ":")
	return len(parts) >= 5 && !strings.Contains(payload, "=") && isDigits(parts[1])
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func parseColonWdtt(payload, name string) (wdttLinkParsed, error) {
	parts := strings.Split(payload, ":")
	if len(parts) < 5 {
		return wdttLinkParsed{}, fmt.Errorf("invalid colon wdtt link")
	}
	var hashes []string
	if len(parts) > 5 && parts[5] != "" {
		for _, h := range strings.Split(parts[5], ",") {
			if t := strings.TrimSpace(h); t != "" {
				hashes = append(hashes, t)
			}
		}
	}
	return wdttLinkParsed{
		IP:       parts[0],
		DtlsPort: parts[1],
		Password: parts[4],
		Name:     name,
		Hashes:   hashes,
	}, nil
}

func parseJSONWdtt(payload string) (wdttLinkParsed, error) {
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return wdttLinkParsed{}, err
		}
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return wdttLinkParsed{}, err
	}
	ip := strings.TrimSpace(fmt.Sprint(firstStr(raw, "ip", "add")))
	pass := strings.TrimSpace(fmt.Sprint(firstStr(raw, "pass", "id")))
	name := strings.TrimSpace(fmt.Sprint(firstStr(raw, "name", "ps", "remark", "email")))
	vpnName := strings.TrimSpace(fmt.Sprint(firstStr(raw, "vpn", "VPN")))
	if name == "" {
		name = "Server"
	}
	dtls := int64(0)
	switch v := raw["dtls"].(type) {
	case float64:
		dtls = int64(v)
	case json.Number:
		dtls, _ = v.Int64()
	}
	if ip == "" || pass == "" || dtls <= 0 {
		return wdttLinkParsed{}, fmt.Errorf("incomplete json wdtt link")
	}
	var hashes []string
	if h := firstStr(raw, "hash", "vk_hash"); h != "" {
		for _, part := range strings.Split(h, ",") {
			if t := strings.TrimSpace(part); t != "" {
				hashes = append(hashes, t)
			}
		}
	}
	deviceID := firstStr(raw, "did", "device_id")
	return wdttLinkParsed{
		IP:       ip,
		DtlsPort: strconv.FormatInt(dtls, 10),
		Password: pass,
		Name:     name,
		VpnName:  vpnName,
		Hashes:   hashes,
		SubURL:   normalizeSubURL(firstStr(raw, "sub", "subUrl", "sub_url")),
		DeviceID: deviceID,
	}, nil
}

func normalizeSubURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(raw), "http://") && !strings.HasPrefix(strings.ToLower(raw), "https://") {
		return ""
	}
	return strings.Split(raw, "?")[0]
}

func firstStr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}
