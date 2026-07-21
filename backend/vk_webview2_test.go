package backend

import "testing"

func TestVKRemixsidIsNew(t *testing.T) {
	cases := []struct {
		found, baseline string
		want            bool
	}{
		{"abc", "", true},
		{"abc", "abc", false},
		{"abc", "def", true},
		{"", "abc", false},
	}
	for _, c := range cases {
		got := vkRemixsidIsNew(c.found, c.baseline)
		if got != c.want {
			t.Fatalf("vkRemixsidIsNew(%q, %q) = %v, want %v", c.found, c.baseline, got, c.want)
		}
	}
}

func TestVKLoginCookiesReady(t *testing.T) {
	cases := []struct {
		remixsid, baseline, p string
		want                    bool
	}{
		{"abc", "", "p1", true},
		{"abc", "abc", "p1", false},
		{"abc", "", "", false},
		{"", "", "p1", false},
	}
	for _, c := range cases {
		got := vkLoginCookiesReady(c.remixsid, c.baseline, c.p)
		if got != c.want {
			t.Fatalf("vkLoginCookiesReady(%q,%q,%q) = %v, want %v", c.remixsid, c.baseline, c.p, got, c.want)
		}
	}
}

func TestVKCookieDomainOK(t *testing.T) {
	if !vkCookieDomainOK(".vk.ru", ".vk.com", ".vk.ru") {
		t.Fatal("expected .vk.ru ok")
	}
	if !vkCookieDomainOK("vk.com", ".vk.com", ".vk.ru") {
		t.Fatal("expected vk.com ok")
	}
	if vkCookieDomainOK(".evil.com", ".vk.com", ".vk.ru") {
		t.Fatal("evil domain")
	}
}

func TestVKLoginURLStillAuthFlow(t *testing.T) {
	auth := []string{
		"",
		"about:blank",
		"https://id.vk.ru/qr_auth",
		"https://login.vk.ru/?act=login",
		"https://oauth.vk.com/authorize",
		"https://vk.ru/login",
	}
	for _, u := range auth {
		if !vkLoginURLStillAuthFlow(u) {
			t.Fatalf("expected auth flow: %q", u)
		}
	}
}

func TestVKLoginURLLooksLoggedIn(t *testing.T) {
	// QR / login wall on site root — classic path check stays strict
	wall := []string{
		"https://vk.ru/",
		"https://vk.com/",
		"https://vk.ru",
		"https://id.vk.ru/qr",
		"https://login.vk.ru/?act=login",
		"https://vk.ru/index.php",
	}
	for _, u := range wall {
		if vkLoginURLLooksLoggedIn(u) {
			t.Fatalf("login wall must not look logged-in: %q", u)
		}
	}
	ok := []string{
		"https://vk.ru/feed",
		"https://vk.ru/im",
		"https://vk.com/feed",
		"https://vk.ru/id1",
		"https://m.vk.ru/friends",
	}
	for _, u := range ok {
		if !vkLoginURLLooksLoggedIn(u) {
			t.Fatalf("expected logged-in: %q", u)
		}
	}
}

func TestVKLoginURLAllowsCookieHarvest(t *testing.T) {
	// Root QR page: cookies+web_token may complete login without /feed redirect.
	if !vkLoginURLAllowsCookieHarvest("https://vk.ru/") {
		t.Fatal("vk.ru/ must allow cookie harvest")
	}
	if !vkLoginURLAllowsCookieHarvest("https://vk.ru/feed") {
		t.Fatal("feed must allow")
	}
	blocked := []string{
		"https://id.vk.ru/qr",
		"https://login.vk.ru/?act=login",
		"https://oauth.vk.com/authorize",
		"",
	}
	for _, u := range blocked {
		if vkLoginURLAllowsCookieHarvest(u) {
			t.Fatalf("must block auth flow: %q", u)
		}
	}
}
