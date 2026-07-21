package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateFromLegacy(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "AppData", "pwdtt")
	oldUpdate := filepath.Join(base, "Local", "WDTT", "update")
	root := filepath.Join(base, "portable", "data")
	_ = os.MkdirAll(filepath.Join(legacy, "profiles"), 0755)
	_ = os.MkdirAll(filepath.Join(legacy, "secrets"), 0700)
	_ = os.MkdirAll(oldUpdate, 0700)
	_ = os.WriteFile(filepath.Join(legacy, "profiles", "srv.json"), []byte(`{"peer":"1.2.3.4"}`), 0600)
	_ = os.WriteFile(filepath.Join(legacy, "secrets", "cookies-vk.json"), []byte("remixsid=abc"), 0600)
	_ = os.WriteFile(filepath.Join(oldUpdate, "pending.json"), []byte(`{"version":"v0.3.1"}`), 0600)

	_ = os.MkdirAll(root, 0755)
	migrateFromLegacy(root, legacy, oldUpdate)

	if !fileExists(filepath.Join(root, migrateMarkerName)) {
		t.Fatal("expected migrate marker")
	}
	raw, err := os.ReadFile(filepath.Join(root, "profiles", "srv.json"))
	if err != nil || string(raw) != `{"peer":"1.2.3.4"}` {
		t.Fatalf("profile: %s err=%v", raw, err)
	}
	raw, err = os.ReadFile(filepath.Join(root, "secrets", "cookies-vk.json"))
	if err != nil || string(raw) != "remixsid=abc" {
		t.Fatalf("cookies: %s err=%v", raw, err)
	}
	raw, err = os.ReadFile(filepath.Join(root, "update", "pending.json"))
	if err != nil || string(raw) != `{"version":"v0.3.1"}` {
		t.Fatalf("update: %s err=%v", raw, err)
	}

	// Second run: do not overwrite portable edits.
	_ = os.WriteFile(filepath.Join(root, "profiles", "srv.json"), []byte(`{"peer":"kept"}`), 0600)
	migrateFromLegacy(root, legacy, oldUpdate)
	raw, _ = os.ReadFile(filepath.Join(root, "profiles", "srv.json"))
	if string(raw) != `{"peer":"kept"}` {
		t.Fatalf("portable file overwritten: %s", raw)
	}
}

func TestCopyDirSkipsExisting(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("new"), 0600)
	_ = os.WriteFile(filepath.Join(dst, "a.txt"), []byte("old"), 0600)
	n, err := copyDirContents(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected skip existing, got n=%d", n)
	}
	raw, _ := os.ReadFile(filepath.Join(dst, "a.txt"))
	if string(raw) != "old" {
		t.Fatalf("got %q", raw)
	}
}
