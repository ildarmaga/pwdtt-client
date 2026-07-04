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

func vkRemixsidIsNew(found, baseline string) bool {
	return found != "" && found != baseline
}
