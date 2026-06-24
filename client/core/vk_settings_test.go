package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func resetVKAuthSettings() {
	vkUseCookies.Store(false)
}

func TestVKUseCookiesDefaultOff(t *testing.T) {
	dir := t.TempDir()
	oldSettings := vkSettingsPath
	oldCookies := vkCookiesPath
	vkSettingsPath = func() string { return filepath.Join(dir, "settings", "vk-auth.json") }
	vkCookiesPath = func() string { return filepath.Join(dir, "secrets", "cookies-vk.json") }
	defer func() {
		vkSettingsPath = oldSettings
		vkCookiesPath = oldCookies
		resetVKAuthSettings()
	}()

	if err := os.MkdirAll(filepath.Dir(vkCookiesPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vkCookiesPath(), []byte("remixsid=test123"), 0600); err != nil {
		t.Fatal(err)
	}

	resetVKAuthSettings()
	loadVKAuthSettings()

	if VKUseCookies() {
		t.Fatal("expected anonymous by default even when cookies file exists")
	}
}

func TestVKUseCookiesToggleOnUsesCookiesOnly(t *testing.T) {
	dir := t.TempDir()
	oldSettings := vkSettingsPath
	vkSettingsPath = func() string { return filepath.Join(dir, "vk-auth.json") }
	defer func() {
		vkSettingsPath = oldSettings
		resetVKAuthSettings()
	}()

	if err := SetVKUseCookies(true); err != nil {
		t.Fatal(err)
	}
	if !VKUseCookies() {
		t.Fatal("expected true after SetVKUseCookies(true)")
	}
	loadVKAuthSettings()
	if !VKUseCookies() {
		t.Fatal("expected persisted true")
	}
}

func TestVKUseCookiesToggleOffAfterOn(t *testing.T) {
	dir := t.TempDir()
	oldSettings := vkSettingsPath
	vkSettingsPath = func() string { return filepath.Join(dir, "vk-auth.json") }
	defer func() {
		vkSettingsPath = oldSettings
		resetVKAuthSettings()
	}()

	if err := SetVKUseCookies(true); err != nil {
		t.Fatal(err)
	}
	if err := SetVKUseCookies(false); err != nil {
		t.Fatal(err)
	}
	if VKUseCookies() {
		t.Fatal("expected false after explicit toggle off")
	}
}

func TestVKCookiesStatusAnonymousHint(t *testing.T) {
	resetVKAuthSettings()
	_, hint := VKCookiesStatus()
	if hint == "" {
		t.Fatal("expected hint")
	}
}

func TestIsVKParticipantFlood(t *testing.T) {
	if !isVKParticipantFlood(fmt.Errorf("joinConversationByLink: error.webrtc.participant.check.flood")) {
		t.Fatal("expected flood")
	}
}

func TestIsVKBrokenToken(t *testing.T) {
	if !isVKBrokenToken(fmt.Errorf("AUTH_LOGIN : Access token is broken")) {
		t.Fatal("expected broken token")
	}
}
