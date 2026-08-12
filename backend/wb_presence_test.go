package backend

import "testing"

func TestWbAuthURLFromSubURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://panel.example/sub/abc123", "https://panel.example/sub/wb/auth"},
		{"https://panel.example/sub/abc123/", "https://panel.example/sub/wb/auth"},
		{"https://panel.example/sub/abc123?format=qwdtt", "https://panel.example/sub/wb/auth"},
		{"https://panel.example/sub/wb/auth", "https://panel.example/sub/wb/auth"},
	}
	for _, tc := range tests {
		if got := wbAuthURLFromSubURL(tc.in); got != tc.want {
			t.Fatalf("wbAuthURLFromSubURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
