package backend

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// subIDToken — как genSubID в панели WDTT (16 символов a-z0-9).
var subIDToken = regexp.MustCompile(`^[a-z0-9]{8,32}$`)

// IsPanelSubURL — ссылка подписки WDTT-панели: https://host[:port]/любой/путь/TOKEN
// Путь и порт настраиваются в панели (subPath, subPort, subURI).
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
	if len(parts) < 1 {
		return false
	}
	token := parts[len(parts)-1]
	return subIDToken.MatchString(token)
}

func validatePanelSubURL(raw string) error {
	if !IsPanelSubURL(raw) {
		return fmt.Errorf("нужна ссылка подписки WDTT-панели (https://…/TOKEN)")
	}
	return nil
}

// ExtractSubURLFromWdttLink — wdtt:// с полем sub внутри JSON (из панели).
func ExtractSubURLFromWdttLink(link string) (string, error) {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "wdtt://") {
		return "", fmt.Errorf("not wdtt link")
	}
	parsed, err := parseWdttLink(link)
	if err != nil {
		return "", err
	}
	sub := strings.TrimSpace(parsed.SubURL)
	if sub == "" {
		return "", fmt.Errorf("wdtt link has no sub field")
	}
	if !IsPanelSubURL(sub) {
		return "", fmt.Errorf("sub field is not a panel subscription url")
	}
	return sub, nil
}
