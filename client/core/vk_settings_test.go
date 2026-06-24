package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVKUseCookiesDefaultWhenCookiesFileExists(t *testing.T) {
	dir := t.TempDir()
	oldSettings := vkSettingsPath
	oldCookies := vkCookiesPath
	vkSettingsPath = func() string { return filepath.Join(dir, "settings", "vk-auth.json") }
	vkCookiesPath = func() string { return filepath.Join(dir, "secrets", "cookies-vk.json") }
	defer func() {
		vkSettingsPath = oldSettings
		vkCookiesPath = oldCookies
		vkSettingsExplicit.Store(false)
		vkUseCookies.Store(false)
	}()

	if err := os.MkdirAll(filepath.Dir(vkCookiesPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vkCookiesPath(), []byte("remixsid=test123"), 0600); err != nil {
		t.Fatal(err)
	}

	vkSettingsExplicit.Store(false)
	vkUseCookies.Store(false)
	initVKAuthDefaults()

	if !VKUseCookies() {
		t.Fatal("expected cookies auth default true when cookies file exists and no settings")
	}
}

func TestVKUseCookiesExplicitOptOut(t *testing.T) {
	dir := t.TempDir()
	oldSettings := vkSettingsPath
	oldCookies := vkCookiesPath
	vkSettingsPath = func() string { return filepath.Join(dir, "settings", "vk-auth.json") }
	vkCookiesPath = func() string { return filepath.Join(dir, "secrets", "cookies-vk.json") }
	defer func() {
		vkSettingsPath = oldSettings
		vkCookiesPath = oldCookies
		vkSettingsExplicit.Store(false)
		vkUseCookies.Store(false)
	}()

	if err := os.MkdirAll(filepath.Dir(vkCookiesPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vkCookiesPath(), []byte("remixsid=test123"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetVKUseCookies(false); err != nil {
		t.Fatal(err)
	}
	if VKUseCookies() {
		t.Fatal("expected explicit opt-out to disable cookies auth")
	}
}

func TestVKUseCookiesTogglePersist(t *testing.T) {
	dir := t.TempDir()
	old := vkSettingsPath
	vkSettingsPath = func() string { return filepath.Join(dir, "vk-auth.json") }
	defer func() {
		vkSettingsPath = old
		vkSettingsExplicit.Store(false)
		_ = SetVKUseCookies(false)
	}()

	if err := SetVKUseCookies(true); err != nil {
		t.Fatal(err)
	}
	if !VKUseCookiesExplicit() {
		t.Fatal("expected explicit true")
	}
	loadVKAuthSettings()
	if !VKUseCookiesExplicit() {
		t.Fatal("expected persisted true")
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
