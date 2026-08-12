package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVKCookiesStatusFromPayloadEmpty(t *testing.T) {
	ok, hint := VKCookiesStatusFromPayload("")
	if ok {
		t.Fatal("expected ok=false for empty payload")
	}
	if hint == "" {
		t.Fatal("expected meaningful Russian hint for empty payload")
	}
}

func TestVKCookiesStatusFromPayloadInvalidJSON(t *testing.T) {
	ok, hint := VKCookiesStatusFromPayload(`{not-json`)
	if ok {
		t.Fatal("expected ok=false for invalid JSON")
	}
	if hint == "" {
		t.Fatal("expected meaningful Russian hint for parse failure")
	}
}

func TestVKCookiesStatusFromPayloadMissingRemixsid(t *testing.T) {
	ok, hint := VKCookiesStatusFromPayload(`[{"name":"remixlang","value":"0"}]`)
	if ok {
		t.Fatal("expected ok=false when remixsid missing")
	}
	if hint == "" {
		t.Fatal("expected meaningful Russian hint when remixsid missing")
	}
}

func TestVKCookiesStatusFromPayloadValidateSuccess(t *testing.T) {
	dir := t.TempDir()
	oldSettings := vkSettingsPath
	vkSettingsPath = func() string { return filepath.Join(dir, "vk-auth.json") }
	oldValidate := vkCookieValidateLive
	vkCookieValidateLive = func(string) error { return nil }
	defer func() {
		vkSettingsPath = oldSettings
		vkCookieValidateLive = oldValidate
		resetVKAuthSettings()
	}()
	resetVKAuthSettings()

	ok, hint := VKCookiesStatusFromPayload("remixsid=validtoken; remixlang=0")
	if !ok {
		t.Fatalf("expected ok=true, hint=%q", hint)
	}
	if hint != "Cookies действительны (тумблер выключен)" {
		t.Fatalf("expected toggle-off hint, got %q", hint)
	}

	if err := SetVKUseCookies(true); err != nil {
		t.Fatal(err)
	}
	ok, hint = VKCookiesStatusFromPayload("remixsid=validtoken; remixlang=0")
	if !ok {
		t.Fatalf("expected ok=true with toggle on, hint=%q", hint)
	}
	if hint != "Cookies действительны" {
		t.Fatalf("expected valid hint, got %q", hint)
	}
}

func TestVKCookiesStatusFromPayloadValidateFail(t *testing.T) {
	oldValidate := vkCookieValidateLive
	vkCookieValidateLive = func(string) error { return fmt.Errorf("empty access_token") }
	defer func() { vkCookieValidateLive = oldValidate }()

	ok, hint := VKCookiesStatusFromPayload("remixsid=expired; remixlang=0")
	if ok {
		t.Fatal("expected ok=false when validate fails")
	}
	if hint != vkCookieExpiredHint {
		t.Fatalf("expected expired hint %q, got %q", vkCookieExpiredHint, hint)
	}
}

func TestVKCookiesStatusFromPayloadDoesNotUseFile(t *testing.T) {
	dir := t.TempDir()
	oldCookies := vkCookiesPath
	oldSettings := vkSettingsPath
	vkCookiesPath = func() string { return filepath.Join(dir, "secrets", "cookies-vk.json") }
	vkSettingsPath = func() string { return filepath.Join(dir, "settings", "vk-auth.json") }
	oldValidate := vkCookieValidateLive
	vkCookieValidateLive = func(string) error { return nil }
	defer func() {
		vkCookiesPath = oldCookies
		vkSettingsPath = oldSettings
		vkCookieValidateLive = oldValidate
		resetVKAuthSettings()
	}()
	resetVKAuthSettings()

	if err := os.MkdirAll(filepath.Dir(vkCookiesPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vkCookiesPath(), []byte("remixsid=fromfile"), 0600); err != nil {
		t.Fatal(err)
	}

	ok, hint := VKCookiesStatusFromPayload("")
	if ok {
		t.Fatalf("empty draft must not read file; hint=%q", hint)
	}
}
