//go:build windows

package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"wg-turn-client/core"
)

var vkLoginWin struct {
	sync.Mutex
	cancel  context.CancelFunc
	status  string
	errMsg  string
	done    bool
	cookie  string
	active  bool
}

func findEdgeBrowser() string {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// vkVisibleChromedpOptions — visible Edge window (DefaultExecAllocatorOptions includes Headless).
func vkVisibleChromedpOptions(edge, profile string) []chromedp.ExecAllocatorOption {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(edge),
		chromedp.Flag("user-data-dir", profile),
		chromedp.Flag("headless", false),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.Flag("mute-audio", false),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("new-window", true),
		chromedp.WindowSize(520, 720),
	)
	return opts
}

func (a *App) StartVKLogin() (VKLoginStartResult, error) {
	vkLoginWin.Lock()
	defer vkLoginWin.Unlock()

	if vkLoginWin.active {
		return VKLoginStartResult{Active: true, Native: true}, nil
	}

	edge := findEdgeBrowser()
	if edge == "" {
		return VKLoginStartResult{}, fmt.Errorf("Microsoft Edge не найден — установите Edge")
	}

	ctx, cancel := context.WithCancel(a.ctx)
	vkLoginWin.cancel = cancel
	vkLoginWin.active = true
	vkLoginWin.done = false
	vkLoginWin.errMsg = ""
	vkLoginWin.cookie = ""
	vkLoginWin.status = "Загрузка VK…"

	go a.runVKLoginBrowser(ctx, edge)
	return VKLoginStartResult{Active: true, Native: true}, nil
}

func (a *App) StopVKLogin() {
	vkLoginWin.Lock()
	cancel := vkLoginWin.cancel
	vkLoginWin.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) runVKLoginBrowser(ctx context.Context, edge string) {
	defer func() {
		vkLoginWin.Lock()
		vkLoginWin.active = false
		vkLoginWin.cancel = nil
		vkLoginWin.Unlock()
	}()

	profile := filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "profile")
	_ = os.MkdirAll(profile, 0700)

	opts := vkVisibleChromedpOptions(edge, profile)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	if err := chromedp.Run(browserCtx, chromedp.Navigate("https://vk.com/")); err != nil {
		vkLoginWin.Lock()
		vkLoginWin.errMsg = "Не удалось открыть vk.com: " + err.Error()
		vkLoginWin.Unlock()
		return
	}

	vkLoginWin.Lock()
	vkLoginWin.status = "Войдите в VK — cookies сохранятся автоматически"
	vkLoginWin.Unlock()

	ticker := time.NewTicker(1200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			header, ok := vkHarvestChromedp(browserCtx)
			if !ok {
				continue
			}
			vkLoginWin.Lock()
			vkLoginWin.done = true
			vkLoginWin.cookie = header
			vkLoginWin.status = "Cookies сохранены"
			vkLoginWin.Unlock()
			cancelBrowser()
			return
		}
	}
}

func vkHarvestChromedp(ctx context.Context) (string, bool) {
	var cookies []*network.Cookie
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().WithUrls([]string{
			"https://vk.com/",
			"https://login.vk.com/",
			"https://id.vk.com/",
		}).Do(ctx)
		return err
	})); err != nil {
		return "", false
	}
	var remixsid, pCookie string
	for _, c := range cookies {
		dom := strings.ToLower(c.Domain)
		if !strings.HasPrefix(dom, ".") {
			dom = "." + dom
		}
		if c.Name == "remixsid" && strings.HasSuffix(dom, ".vk.com") && c.Value != "" {
			remixsid = c.Value
		}
		if c.Name == "p" && strings.HasSuffix(dom, ".login.vk.com") && c.Value != "" {
			pCookie = c.Value
		}
	}
	if remixsid == "" {
		return "", false
	}
	header := "remixsid=" + remixsid
	if pCookie != "" {
		header += "; p=" + pCookie
	}
	return header, true
}

func (a *App) PollVKLogin() VKLoginPollResult {
	vkLoginWin.Lock()
	defer vkLoginWin.Unlock()

	if !vkLoginWin.active && !vkLoginWin.done {
		return VKLoginPollResult{Status: "idle"}
	}
	if vkLoginWin.errMsg != "" {
		return VKLoginPollResult{Status: "error", Message: vkLoginWin.errMsg}
	}
	if vkLoginWin.done && vkLoginWin.cookie != "" {
		if err := core.SaveVKCookiesJSON([]byte(vkLoginWin.cookie)); err != nil {
			return VKLoginPollResult{Status: "error", Message: err.Error()}
		}
		_ = core.SetVKUseCookies(true)
		vkLoginWin.done = false
		vkLoginWin.cookie = ""
		return VKLoginPollResult{Done: true, Status: "done", Message: "Cookies сохранены"}
	}
	msg := vkLoginWin.status
	if msg == "" {
		msg = "Войдите в VK — ожидаем remixsid…"
	}
	return VKLoginPollResult{Status: "waiting", Message: msg}
}
