//go:build windows

package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

func vkLoginProfileDir() string {
	return filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", fmt.Sprintf("session-%d", time.Now().UnixNano()))
}

func vkLoginLogPath() string {
	return filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "edge.log")
}

func vkVisibleChromedpOptions(edge, profile string) []chromedp.ExecAllocatorOption {
	return []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(edge),
		chromedp.UserDataDir(profile),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.WindowSize(520, 720),
		chromedp.Flag("window-position", "200,100"),
		chromedp.Flag("headless", false),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.Flag("mute-audio", false),
		chromedp.Flag("edge-skip-compat-layer-relaunch", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("remote-allow-origins", "*"),
	}
}

func startVKChromedp(parent context.Context, edge, profile string) (browserCtx context.Context, cleanup func(), err error) {
	if err := os.MkdirAll(profile, 0700); err != nil {
		return nil, nil, err
	}
	clearEdgeProfileLocks(profile)

	logFile, logErr := os.OpenFile(vkLoginLogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)

	opts := vkVisibleChromedpOptions(edge, profile)
	opts = append(opts, chromedp.WSURLReadTimeout(45*time.Second))
	if logErr == nil && logFile != nil {
		opts = append(opts, chromedp.CombinedOutput(logFile))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parent, opts...)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)

	cleanup = func() {
		cancelBrowser()
		cancelAlloc()
		if logFile != nil {
			_ = logFile.Close()
		}
	}

	navCtx, cancelNav := context.WithTimeout(browserCtx, 60*time.Second)
	defer cancelNav()

	if err := chromedp.Run(navCtx, chromedp.Navigate("https://vk.com/")); err != nil {
		cleanup()
		return nil, nil, err
	}
	return browserCtx, cleanup, nil
}

func formatVKChromedpStartErr(err error) string {
	errStr := err.Error()
	logPath := vkLoginLogPath()
	if strings.Contains(errStr, "failed to start") || strings.Contains(errStr, "exit status") {
		tail := errStr
		if len(tail) > 120 {
			tail = "…" + tail[len(tail)-120:]
		}
		return fmt.Sprintf("Edge не запустился — закройте другие окна Edge. Лог: %s. %s", logPath, tail)
	}
	return "Не удалось открыть vk.com: " + errStr
}
