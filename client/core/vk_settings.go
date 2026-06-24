package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
)

type vkAuthSettings struct {
	UseCookies bool `json:"use_cookies"`
}

var (
	vkUseCookies       atomic.Bool
	vkSettingsExplicit atomic.Bool
)

var vkSettingsPath = func() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.Getenv("HOME")
	}
	return filepath.Join(base, "pwdtt", "settings", "vk-auth.json")
}

func loadVKAuthSettings() {
	raw, err := os.ReadFile(vkSettingsPath())
	if err != nil {
		return
	}
	var s vkAuthSettings
	if json.Unmarshal(raw, &s) != nil {
		return
	}
	vkSettingsExplicit.Store(true)
	vkUseCookies.Store(s.UseCookies)
}

func initVKAuthDefaults() {
	if vkSettingsExplicit.Load() {
		return
	}
	// v0.3.41 compat: cookies on disk → cookie auth by default (anonymous join dead at okcdn).
	if header, err := LoadVKCookieHeader(); err == nil && header != "" {
		vkUseCookies.Store(true)
	}
}

func saveVKAuthSettings(useCookies bool) error {
	dir := filepath.Dir(vkSettingsPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(vkAuthSettings{UseCookies: useCookies})
	if err != nil {
		return err
	}
	return os.WriteFile(vkSettingsPath(), raw, 0600)
}

func init() {
	loadVKAuthSettings()
	initVKAuthDefaults()
}

// VKUseCookies reports whether remixsid cookie auth is enabled (explicit or default).
func VKUseCookies() bool {
	return vkUseCookiesEffective()
}

func vkUseCookiesEffective() bool {
	header, err := LoadVKCookieHeader()
	if err != nil || header == "" {
		return false
	}
	if !vkSettingsExplicit.Load() {
		return true
	}
	return vkUseCookies.Load()
}

// VKUseCookiesExplicit returns the stored toggle (false when user never saved settings).
func VKUseCookiesExplicit() bool {
	if !vkSettingsExplicit.Load() {
		return false
	}
	return vkUseCookies.Load()
}

// SetVKUseCookies toggles cookie-based VK auth.
func SetVKUseCookies(v bool) error {
	vkUseCookies.Store(v)
	vkSettingsExplicit.Store(true)
	return saveVKAuthSettings(v)
}
