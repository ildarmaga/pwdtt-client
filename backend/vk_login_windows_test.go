//go:build windows

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

	for _, a := range gotArgs {
		if a == "--headless" || len(a) >= 10 && a[:10] == "--headless" {
			t.Fatalf("unexpected headless arg in cmd args: %q", a)
		}
	}
}

func TestClearEdgeProfileLocks(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "SingletonLock")
	if err := os.WriteFile(lockPath, []byte("lock"), 0600); err != nil {
		t.Fatalf("failed to write dummy lock: %v", err)
	}
	// ensure file exists
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock to exist: %v", err)
	}

	clearEdgeProfileLocks(dir)

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("expected lock to be removed, stat error: %v", err)
	}
}

