//go:build windows

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

func TestVkVisibleChromedpOptionsNoHeadless(t *testing.T) {
	var gotArgs []string
	// Use a non-existent edge path so the browser won't actually start successfully,
	// but ModifyCmdFunc should still receive the prepared *exec.Cmd.
	edge := filepath.Join("C:\\", "nonexistent", "msedge.exe")
	profile := filepath.Join(os.TempDir(), "pwdtt-test-profile")

	opts := vkVisibleChromedpOptions(edge, profile)
	opts = append(opts, chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
		gotArgs = cmd.Args
	}))

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Running will attempt to start the browser and invoke ModifyCmdFunc.
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(context.Context) error { return nil }))

	if gotArgs == nil {
		t.Fatalf("ModifyCmdFunc was not invoked; gotArgs is nil")
	}

	hasEdgeSkip := false
	for _, a := range gotArgs {
		if a == "--headless" || len(a) >= 10 && a[:10] == "--headless" {
			t.Fatalf("unexpected headless arg in cmd args: %q", a)
		}
		if a == "--no-sandbox" || strings.HasPrefix(a, "--no-sandbox") {
			t.Fatalf("unexpected no-sandbox arg in cmd args: %q", a)
		}
		if a == "--edge-skip-compat-layer-relaunch" || strings.Contains(a, "edge-skip-compat-layer-relaunch") {
			hasEdgeSkip = true
		}
	}
	if !hasEdgeSkip {
		t.Fatalf("expected edge-skip-compat-layer-relaunch in cmd args: %v", gotArgs)
	}
}

func TestClearEdgeProfileLocks(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"SingletonLock", "DevToolsActivePort"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("lock"), 0600); err != nil {
			t.Fatalf("failed to write dummy %s: %v", name, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}

	clearEdgeProfileLocks(dir)

	for _, name := range []string{"SingletonLock", "DevToolsActivePort"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat error: %v", name, err)
		}
	}
}

