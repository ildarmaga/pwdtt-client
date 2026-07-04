//go:build windows

package backend

import (
	"path/filepath"
	"testing"
)

func TestVKLoginStatusRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")

	want := vkLoginStatusFile{
		Done:    true,
		Status:  "done",
		Message: "Cookies сохранены",
		Cookie:  "remixsid=abc; p=xyz",
		Pid:     4242,
	}
	writeVKLoginStatus(path, want)

	got, err := readVKLoginStatus(path)
	if err != nil {
		t.Fatalf("readVKLoginStatus: %v", err)
	}
	if got != want {
		t.Fatalf("roundtrip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}
