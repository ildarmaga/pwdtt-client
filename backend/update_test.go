package backend

import "testing"

func TestCompareVersionTags(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.3.89", "0.3.90", -1},
		{"v0.3.90", "v0.3.89", 1},
		{"0.3.90", "0.3.90", 0},
		{"0.3.9", "0.3.10", -1},
	}
	for _, c := range cases {
		got := compareVersionTags(c.a, c.b)
		if got != c.want {
			t.Fatalf("%q vs %q: got %d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("0.3.89", "v0.3.90") {
		t.Fatal("expected update available")
	}
	if versionLess("0.3.90", "0.3.90") {
		t.Fatal("same version should not be less")
	}
}

func TestWindowsDownloadURLRequiresAsset(t *testing.T) {
	empty := ghRelease{TagName: "v0.3.235", Assets: nil}
	if windowsDownloadURL(empty) != "" {
		t.Fatal("empty assets must not invent a URL (causes HTTP 404)")
	}
	ok := ghRelease{TagName: "v0.3.234", Assets: []ghReleaseAsset{
		{Name: "wdtt-windows-amd64.exe", BrowserDownloadURL: "https://example/x.exe"},
	}}
	if windowsDownloadURL(ok) != "https://example/x.exe" {
		t.Fatal("expected asset URL")
	}
}
