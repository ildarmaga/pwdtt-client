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
