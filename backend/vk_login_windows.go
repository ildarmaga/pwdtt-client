//go:build windows

package backend

import (
	"context"
	"encoding/json"
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
	cancel    context.CancelFunc
	status    string
	errMsg    string
	done      bool
	cookie    string
	active    bool
	helperPid uint32
}

type vkLoginStatusFile struct {
	Done    bool   `json:"done"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Cookie  string `json:"cookie,omitempty"`
	Pid     int    `json:"pid,omitempty"`
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

func clearEdgeProfileLocks(profile string) {
	for _, name := range []string{"SingletonLock", "SingletonCookie", "lockfile", "DevToolsActivePort"} {
		_ = os.Remove(filepath.Join(profile, name))
	}
}

func vkVisibleChromedpOptions(edge, profile string) []chromedp.ExecAllocatorOption {
	return append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(edge),
		chromedp.UserDataDir(profile),
		chromedp.NoSandbox,
		chromedp.WindowSize(520, 720),
		chromedp.Flag("window-position", "200,100"),
		chromedp.Flag("headless", false),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.Flag("mute-audio", false),
		chromedp.Flag("edge-skip-compat-layer-relaunch", true),
		chromedp.Flag("disable-gpu", true),
	)
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
	vkLoginWin.helperPid = 0
	vkLoginWin.status = "Загрузка VK…"

	if isProcessElevated() {
		go a.runVKLoginHelper(ctx)
	} else {
		go a.runVKLoginBrowser(ctx, edge)
	}
	return VKLoginStartResult{Active: true, Native: true}, nil
}

func (a *App) StopVKLogin() {
	vkLoginWin.Lock()
	cancel := vkLoginWin.cancel
	pid := vkLoginWin.helperPid
	vkLoginWin.Unlock()
	if cancel != nil {
		cancel()
	}
	if pid != 0 {
		killProcessTree(pid)
		return
	}
	statusPath := filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "status.json")
	if st, err := readVKLoginStatus(statusPath); err == nil && st.Pid > 0 {
		killProcessTree(uint32(st.Pid))
	}
}

func (a *App) runVKLoginHelper(ctx context.Context) {
	defer func() {
		vkLoginWin.Lock()
		vkLoginWin.active = false
		vkLoginWin.cancel = nil
		vkLoginWin.helperPid = 0
		vkLoginWin.Unlock()
	}()

	exe, err := os.Executable()
	if err != nil {
		vkLoginWin.Lock()
		vkLoginWin.errMsg = err.Error()
		vkLoginWin.Unlock()
		return
	}

	profile := filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "profile")
	statusPath := filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "status.json")
	_ = os.MkdirAll(filepath.Dir(statusPath), 0700)
	_ = os.Remove(statusPath)

	pid, err := runDeElevated(exe, []string{
		vkLoginWorkerFlag,
		"-status", statusPath,
		"-data", profile,
	}, filepath.Dir(exe))
	if err != nil {
		vkLoginWin.Lock()
		vkLoginWin.errMsg = "Не удалось запустить окно VK: " + err.Error()
		vkLoginWin.Unlock()
		return
	}

	vkLoginWin.Lock()
	vkLoginWin.helperPid = pid
	vkLoginWin.status = "Открываем Edge…"
	vkLoginWin.Unlock()

	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st, err := readVKLoginStatus(statusPath)
			if err != nil {
				continue
			}
			if st.Pid > 0 && vkLoginWin.helperPid == 0 {
				vkLoginWin.Lock()
				if vkLoginWin.helperPid == 0 {
					vkLoginWin.helperPid = uint32(st.Pid)
				}
				vkLoginWin.Unlock()
			}
			switch st.Status {
			case "error":
				vkLoginWin.Lock()
				vkLoginWin.errMsg = st.Message
				vkLoginWin.Unlock()
				return
			case "done":
				if st.Done && st.Cookie != "" {
					vkLoginWin.Lock()
					vkLoginWin.done = true
					vkLoginWin.cookie = st.Cookie
					vkLoginWin.status = st.Message
					vkLoginWin.Unlock()
				}
				return
			case "cancelled":
				return
			default:
				vkLoginWin.Lock()
				if st.Message != "" {
					vkLoginWin.status = st.Message
				}
				vkLoginWin.Unlock()
			}
		}
	}
}

func readVKLoginStatus(path string) (vkLoginStatusFile, error) {
	var st vkLoginStatusFile
	b, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	return st, nil
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
	clearEdgeProfileLocks(profile)

	logDir := filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk")
	_ = os.MkdirAll(logDir, 0700)
	logFile, logErr := os.OpenFile(filepath.Join(logDir, "edge.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if logErr == nil {
		defer logFile.Close()
	}

	vkLoginWin.Lock()
	vkLoginWin.status = "Открываем Edge…"
	vkLoginWin.Unlock()

	opts := vkVisibleChromedpOptions(edge, profile)
	opts = append(opts, chromedp.WSURLReadTimeout(30*time.Second))
	if logFile != nil {
		opts = append(opts, chromedp.CombinedOutput(logFile))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	navCtx, cancelNav := context.WithTimeout(browserCtx, 45*time.Second)
	defer cancelNav()

	if err := chromedp.Run(navCtx, chromedp.Navigate("https://vk.com/")); err != nil {
		vkLoginWin.Lock()
		if navCtx.Err() == context.DeadlineExceeded {
			vkLoginWin.errMsg = "Edge не запустился за 45 сек — закройте другие окна Edge и попробуйте снова"
		} else {
			errStr := err.Error()
			if strings.Contains(errStr, "failed to start") || strings.Contains(errStr, "exit status") {
				logPath := filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "edge.log")
				tail := errStr
				if len(tail) > 150 {
					tail = "…" + tail[len(tail)-150:]
				}
				vkLoginWin.errMsg = fmt.Sprintf(
					"Edge не запустился. Закройте другие окна Edge и проверьте лог: %s. %s",
					logPath, tail,
				)
			} else {
				vkLoginWin.errMsg = "Не удалось открыть vk.com: " + errStr
			}
		}
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

	if vkLoginWin.errMsg != "" {
		return VKLoginPollResult{Status: "error", Message: vkLoginWin.errMsg}
	}
	if !vkLoginWin.active && !vkLoginWin.done {
		return VKLoginPollResult{Status: "idle"}
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
