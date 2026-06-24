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

var vkUseCookies atomic.Bool

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
	vkUseCookies.Store(s.UseCookies)
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
}

// VKUseCookies — тумблер в настройках: true = только cookie-path, false = anonymous (VK Calls + legacy).
func VKUseCookies() bool {
	return vkUseCookies.Load()
}

// SetVKUseCookies сохраняет выбор пользователя.
func SetVKUseCookies(v bool) error {
	vkUseCookies.Store(v)
	return saveVKAuthSettings(v)
}
