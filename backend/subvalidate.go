package backend

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// subIDToken — как genSubID в панели WDTT (16 символов a-z0-9).
var subIDToken = regexp.MustCompile(`^[a-z0-9]{8,32}$`)

// IsPanelSubURL — только ссылки подписки WDTT-панели (https://host:2096/subs/…).
// Прямой wdtt:// и произвольные URL отклоняются.
func IsPanelSubURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(strings.Split(raw, "?")[0])
	if err != nil {
		return false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	if strings.TrimSpace(u.Host) == "" {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return false
	}
	token := parts[len(parts)-1]
	if !subIDToken.MatchString(token) {
		return false
	}
	prefix := strings.Join(parts[:len(parts)-1], "/")
	return strings.Contains(prefix, "sub")
}

func validatePanelSubURL(raw string) error {
	if !IsPanelSubURL(raw) {
		return fmt.Errorf("нужна ссылка подписки WDTT-панели (https://…/subs/…), не wdtt://")
	}
	return nil
}
