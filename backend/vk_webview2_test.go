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
	if !vkCookieDomainOK(".login.vk.ru", ".login.vk.com", ".login.vk.ru") {
		t.Fatal("expected login.vk.ru ok")
	}
}

func TestVKLoginURLStillAuthFlow(t *testing.T) {
	auth := []string{
		"",
		"about:blank",
		"https://id.vk.ru/qr_auth",
		"https://id.vk.com/auth",
		"https://login.vk.ru/?act=login",
		"https://oauth.vk.com/authorize",
		"https://vk.ru/login",
		"https://id.vk.ru/not_robot_captcha?x=1",
	}
	for _, u := range auth {
		if !vkLoginURLStillAuthFlow(u) {
			t.Fatalf("expected auth flow: %q", u)
		}
	}
	ok := []string{
		"https://vk.ru/",
		"https://vk.ru/feed",
		"https://vk.com/feed",
		"https://m.vk.ru/id1",
	}
	for _, u := range ok {
		if vkLoginURLStillAuthFlow(u) {
			t.Fatalf("expected logged-in page: %q", u)
		}
	}
}
