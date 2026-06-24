package core

import "testing"

func TestVKAuthMode(t *testing.T) {
	resetVKAuthSettings()
	if mode := vkAuthMode(); mode != "anonymous" {
		t.Fatalf("expected anonymous, got %q", mode)
	}
	if err := SetVKUseCookies(true); err != nil {
		t.Fatal(err)
	}
	if mode := vkAuthMode(); mode != "cookies" {
		t.Fatalf("expected cookies, got %q", mode)
	}
}

func vkAuthMode() string {
	if VKUseCookies() {
		return "cookies"
	}
	return "anonymous"
}
