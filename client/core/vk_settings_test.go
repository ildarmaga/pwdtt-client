package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVKUseCookiesToggle(t *testing.T) {
	dir := t.TempDir()
	old := vkSettingsPath
	vkSettingsPath = func() string { return filepath.Join(dir, "vk-auth.json") }
	defer func() {
		vkSettingsPath = old
		_ = SetVKUseCookies(false)
	}()

	if err := SetVKUseCookies(true); err != nil {
		t.Fatal(err)
	}
	if !VKUseCookies() {
		t.Fatal("expected use cookies true")
	}
	loadVKAuthSettings()
	if !VKUseCookies() {
		t.Fatal("expected persisted true")
	}
	if err := SetVKUseCookies(false); err != nil {
		t.Fatal(err)
	}
	if VKUseCookies() {
		t.Fatal("expected false")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "vk-auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("empty settings file")
	}
}
