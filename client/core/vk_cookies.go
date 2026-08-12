package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const vkAuthCookieName = "remixsid"

type vkCookieEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

var vkCookiesPath = func() string {
	return filepath.Join(ConfigRoot(), "secrets", "cookies-vk.json")
}

// LoadVKCookieHeader reads <data>/secrets/cookies-vk.json.
func LoadVKCookieHeader() (string, error) {
	raw, err := os.ReadFile(vkCookiesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("cookies-vk.json не найден")
		}
		return "", err
	}
	return vkParseCookiesPayload(raw)
}

func vkParseCookiesPayload(raw []byte) (string, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "", fmt.Errorf("cookies пусты")
	}
	if strings.HasPrefix(s, "remixsid=") || (strings.Contains(s, ";") && !strings.HasPrefix(s, "[")) {
		return normalizeCookieHeader(s), nil
	}
	var cookies []vkCookieEntry
	if err := json.Unmarshal(raw, &cookies); err != nil {
		return "", fmt.Errorf("неверный JSON cookies: %w", err)
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		parts = append(parts, name+"="+strings.TrimSpace(c.Value))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("cookies пусты")
	}
	header := strings.Join(parts, "; ")
	if !strings.Contains(header, vkAuthCookieName+"=") {
		return "", fmt.Errorf("в cookies нет %s — войдите в VK и экспортируйте cookies", vkAuthCookieName)
	}
	return header, nil
}

// ParseVKCookieHeader parses a cookies draft/file payload into a Cookie header.
func ParseVKCookieHeader(raw string) (string, error) {
	return vkParseCookiesPayload([]byte(raw))
}

func normalizeCookieHeader(s string) string {
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if !strings.Contains(s, vkAuthCookieName+"=") {
		return ""
	}
	return s
}

// SaveVKCookiesJSON persists cookies array or raw string to secrets file.
func SaveVKCookiesJSON(raw []byte) error {
	header, err := vkParseCookiesPayload(raw)
	if err != nil {
		return err
	}
	_ = header
	dir := filepath.Dir(vkCookiesPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	invalidateVKCookieStatusCache()
	if err := os.WriteFile(vkCookiesPath(), raw, 0600); err != nil {
		return err
	}
	return nil
}

// ClearVKCookies removes stored VK cookies.
func ClearVKCookies() error {
	invalidateVKCookieStatusCache()
	err := os.Remove(vkCookiesPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// VKCookiesStatus reports whether remixsid is configured and still valid.
// Hint is always set so the Settings UI can show OK / missing / expired.
func VKCookiesStatus() (ok bool, hint string) {
	header, err := LoadVKCookieHeader()
	hasCookies := err == nil && header != ""
	if !hasCookies {
		return false, "Cookies не заданы — войдите через VK или вставьте вручную"
	}
	if err := vkCookiesLiveValid(header); err != nil {
		return false, vkCookieExpiredHint
	}
	if !VKUseCookies() {
		return true, "Cookies действительны (тумблер выключен)"
	}
	return true, "Cookies действительны"
}

// VKCookiesStatusFromPayload validates a draft cookies payload (Settings textarea)
// without reading the saved secrets file. Uses ValidateVKCookieHeader (no TTL cache).
func VKCookiesStatusFromPayload(raw string) (ok bool, hint string) {
	header, err := vkParseCookiesPayload([]byte(raw))
	if err != nil || header == "" {
		if err != nil {
			return false, err.Error()
		}
		return false, "Cookies не заданы — войдите через VK или вставьте вручную"
	}
	if err := ValidateVKCookieHeader(header); err != nil {
		return false, vkCookieExpiredHint
	}
	if !VKUseCookies() {
		return true, "Cookies действительны (тумблер выключен)"
	}
	return true, "Cookies действительны"
}

// ReadVKCookiesRaw returns the raw secrets file for the Settings textarea.
func ReadVKCookiesRaw() (string, error) {
	raw, err := os.ReadFile(vkCookiesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(raw), nil
}

// VKCookiesPathForUI returns the path shown in settings.
func VKCookiesPathForUI() string {
	return vkCookiesPath()
}
