//go:build windows

package backend

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/chromedp/chromedp"
)

const vkLoginWorkerFlag = "--vk-login-worker"

// MaybeRunVKLoginWorker handles hidden subprocess mode for de-elevated VK login.
// Returns (true, nil) when this invocation was the worker and finished OK.
func MaybeRunVKLoginWorker(args []string) (bool, error) {
	if len(args) == 0 || args[0] != vkLoginWorkerFlag {
		return false, nil
	}
	fs := flag.NewFlagSet("vk-login-worker", flag.ContinueOnError)
	status := fs.String("status", "", "status json path")
	data := fs.String("data", "", "Edge user data dir")
	if err := fs.Parse(args[1:]); err != nil {
		return true, err
	}
	return true, runVKLoginWorker(*status, *data)
}

func runVKLoginWorker(statusPath, profile string) error {
	writeVKLoginStatus(statusPath, vkLoginStatusFile{Status: "waiting", Message: "Загрузка VK…"})

	edge := findEdgeBrowser()
	if edge == "" {
		writeVKLoginStatus(statusPath, vkLoginStatusFile{Status: "error", Message: "Microsoft Edge не найден"})
		return fmt.Errorf("edge not found")
	}

	if profile == "" {
		profile = filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "profile")
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		writeVKLoginStatus(statusPath, vkLoginStatusFile{Status: "error", Message: err.Error()})
		return err
	}
	clearEdgeProfileLocks(profile)

	opts := vkVisibleChromedpOptions(edge, profile)
	opts = append(opts, chromedp.WSURLReadTimeout(30*time.Second))

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	if err := chromedp.Run(ctx, chromedp.Navigate("https://vk.com/")); err != nil {
		writeVKLoginStatus(statusPath, vkLoginStatusFile{Status: "error", Message: "Не удалось открыть vk.com: " + err.Error()})
		return err
	}
	writeVKLoginStatus(statusPath, vkLoginStatusFile{Status: "waiting", Message: "Войдите в VK — cookies сохранятся автоматически"})

	var done atomic.Bool
	ticker := time.NewTicker(1200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if !done.Load() {
				writeVKLoginStatus(statusPath, vkLoginStatusFile{Status: "cancelled", Message: "Вход отменён"})
			}
			return ctx.Err()
		case <-ticker.C:
			header, ok := vkHarvestChromedp(ctx)
			if !ok {
				continue
			}
			done.Store(true)
			writeVKLoginStatus(statusPath, vkLoginStatusFile{
				Done: true, Status: "done", Message: "Cookies сохранены", Cookie: header,
			})
			return nil
		}
	}
}

func writeVKLoginStatus(path string, st vkLoginStatusFile) {
	if path == "" {
		return
	}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(path, b, 0600)
}
