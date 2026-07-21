//go:build windows

package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
	"wg-turn-client/core"
)

const (
	vkWebView2TimerID    = 42
	vkLoginPendingStable = 2 * time.Second
	vkLoginDestroyDelay  = 1500 * time.Millisecond
)

type vkWebView2Session struct {
	chromium *edge.Chromium
	hwnd     win.HWND
	done     atomic.Bool
	navDone  atomic.Bool
	// remixsid seen while still on the login wall — must not close the window.
	baselineRemixsid string
	firstNavAt       time.Time
	lastURL          string
	pendingHeader    string
	pendingSince     time.Time
	doneAt           time.Time
	writeSt          func(vkLoginStatusFile)
	dataDir          string
}

// runVKWebView2Window opens a native window with WebView2 pointed at vk.ru and
// harvests remixsid/p cookies via the WebView2 cookie manager (works with HttpOnly,
// works elevated — no DevTools attach like chromedp). Blocks until the window closes.
func runVKWebView2Window(dataDir string, writeSt func(vkLoginStatusFile)) error {
	runtime.LockOSThread()

	s := &vkWebView2Session{writeSt: writeSt, dataDir: dataDir}

	hInstance := win.GetModuleHandle(nil)
	className, _ := windows.UTF16PtrFromString("WDTTVKLoginWnd")

	wndProc := func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		switch msg {
		case win.WM_SIZE:
			if s.chromium != nil {
				s.chromium.Resize()
			}
		case win.WM_TIMER:
			if wParam == vkWebView2TimerID {
				s.tryHarvest()
				if s.done.Load() {
					if s.doneAt.IsZero() {
						s.doneAt = time.Now()
					} else if time.Since(s.doneAt) >= vkLoginDestroyDelay {
						win.DestroyWindow(hwnd)
					}
				}
			}
		case win.WM_DESTROY:
			if !s.done.Load() {
				writeSt(vkLoginStatusFile{Status: "cancelled", Message: "Вход отменён"})
			}
			win.PostQuitMessage(0)
			return 0
		}
		return win.DefWindowProc(hwnd, msg, wParam, lParam)
	}

	wc := win.WNDCLASSEX{
		HInstance:     hInstance,
		LpszClassName: className,
		LpfnWndProc:   windows.NewCallback(wndProc),
		HCursor:       win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW)),
		HbrBackground: win.HBRUSH(win.COLOR_WINDOW + 1),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	win.RegisterClassEx(&wc)

	const winW, winH = 520, 720
	x := (win.GetSystemMetrics(win.SM_CXSCREEN) - winW) / 2
	y := (win.GetSystemMetrics(win.SM_CYSCREEN) - winH) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	title, _ := windows.UTF16PtrFromString("WDTT — вход VK")
	hwnd := win.CreateWindowEx(
		win.WS_EX_TOPMOST, className, title,
		win.WS_OVERLAPPEDWINDOW,
		x, y, winW, winH,
		0, 0, hInstance, nil,
	)
	if hwnd == 0 {
		writeSt(vkLoginStatusFile{Status: "error", Message: "не удалось создать окно VK"})
		return fmt.Errorf("CreateWindowEx failed")
	}

	chromium := edge.NewChromium()
	chromium.DataPath = dataDir
	chromium.AdditionalBrowserArgs = []string{
		"--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection",
		"--disable-gpu",
		"--disable-gpu-compositing",
	}
	_ = os.MkdirAll(dataDir, 0700)
	vkLoginLog(dataDir, "worker start profile=%s", dataDir)
	// IMPORTANT: go-webview2 reports Resize/Navigate/Eval COM hiccups through
	// this callback (nonFatalErrorCallback). Quitting here was why the login
	// window vanished ~2–3s after open even after the os.Exit(1) fork —
	// a stray WM_SIZE or Navigate during WebView2 bring-up called PostQuitMessage.
	// Bootstrap failures still hard-abort via the library's own os.Exit path.
	chromium.SetErrorCallback(func(err error) {
		vkLoginLog(dataDir, "webview2 warn (ignored): %v", err)
	})
	chromium.ProcessFailedCallback = func(_ *edge.ICoreWebView2, args *edge.ICoreWebView2ProcessFailedEventArgs) {
		var kind edge.COREWEBVIEW2_PROCESS_FAILED_KIND = edge.COREWEBVIEW2_PROCESS_FAILED_KIND_UNKNOWN_PROCESS_EXITED
		if args != nil {
			if k, err := args.GetProcessFailedKind(); err == nil {
				kind = k
			}
		}
		// Only the main browser process dying is unrecoverable (WebView moves to
		// Closed). Renderer/GPU/frame crashes are recoverable — VK's QR canvas is
		// heavy and often kills the render process (blank white QR); previously we
		// quit the whole login window ~1s after it opened. Reload instead so the
		// user can still scan the QR / enter credentials.
		if kind == edge.COREWEBVIEW2_PROCESS_FAILED_KIND_BROWSER_PROCESS_EXITED {
			vkLoginLog(dataDir, "webview2 browser process exited — fatal")
			writeSt(vkLoginStatusFile{Status: "error", Message: "WebView2: процесс браузера завершился — перезапустите вход"})
			win.PostQuitMessage(1)
			return
		}
		reloadURL := s.lastURL
		if reloadURL == "" {
			reloadURL = "https://vk.ru/"
		}
		vkLoginLog(dataDir, "webview2 process failed kind=%d — reloading %q", kind, reloadURL)
		if s.chromium != nil {
			s.chromium.Navigate(reloadURL)
		}
	}
	chromium.NavigationCompletedCallback = func(sender *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		if sender != nil {
			if src, err := sender.GetSource(); err == nil && src != "" {
				s.lastURL = src
			}
		}
		if !s.navDone.Load() {
			s.navDone.Store(true)
			s.firstNavAt = time.Now()
			s.baselineRemixsid = s.readRemixsid()
			vkLoginLog(dataDir, "first navigation done url=%q baseline_remixsid=%q", s.lastURL, s.baselineRemixsid)
		}
		s.tryHarvest()
	}
	s.chromium = chromium
	s.hwnd = hwnd

	if !chromium.Embed(uintptr(hwnd)) {
		writeSt(vkLoginStatusFile{Status: "error", Message: "WebView2 не установлен — установите Microsoft Edge WebView2 Runtime"})
		return fmt.Errorf("webview2 embed failed")
	}
	if settings, err := chromium.GetSettings(); err == nil {
		_ = settings.PutAreDevToolsEnabled(false)
	}
	_ = chromium.Hide()
	_ = chromium.Show()
	chromium.SetBackgroundColour(255, 255, 255, 255)

	win.ShowWindow(hwnd, win.SW_SHOWNORMAL)
	win.ShowWindow(hwnd, win.SW_SHOWNORMAL)
	win.UpdateWindow(hwnd)
	win.SetWindowPos(hwnd, win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_SHOWWINDOW)
	win.SetForegroundWindow(hwnd)
	chromium.Resize()
	// VK migrates vk.com → vk.ru; start on .ru so login/QR and cookies land consistently.
	chromium.Navigate("https://vk.ru/")

	win.SetTimer(hwnd, vkWebView2TimerID, 1500, 0)
	writeSt(vkLoginStatusFile{Status: "waiting", Message: "Войдите в VK — cookies сохранятся автоматически"})

	var msg win.MSG
	for win.GetMessage(&msg, 0, 0, 0) > 0 {
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}
	return nil
}

func (s *vkWebView2Session) readRemixsid() string {
	remixsid, _ := s.readVKCookies()
	return remixsid
}

func (s *vkWebView2Session) readVKCookies() (remixsid, pCookie string) {
	if s.chromium == nil {
		return "", ""
	}
	cm, err := s.chromium.GetCookieManager()
	if err != nil {
		return "", ""
	}
	defer cm.Release()

	uris := []string{
		"https://vk.ru/", "https://vk.com/",
		"https://login.vk.ru/", "https://login.vk.com/",
		"https://id.vk.ru/", "https://id.vk.com/",
		"https://m.vk.ru/", "https://m.vk.com/",
	}
	for _, uri := range uris {
		list, err := cm.GetCookies(uri)
		if err != nil || list == nil {
			continue
		}
		count, _ := list.GetCount()
		for i := uint32(0); i < count; i++ {
			c, err := list.GetItem(i)
			if err != nil || c == nil {
				continue
			}
			name, _ := c.GetName()
			val, _ := c.GetValue()
			dom, _ := c.GetDomain()
			if name == "remixsid" && vkCookieDomainOK(dom, ".vk.com", ".vk.ru") && val != "" {
				remixsid = val
			}
			if name == "p" && vkCookieDomainOK(dom, ".login.vk.com", ".login.vk.ru") && val != "" {
				pCookie = val
			}
			c.Release()
		}
		list.Release()
	}
	return remixsid, pCookie
}

func (s *vkWebView2Session) tryHarvest() {
	if s.done.Load() || s.chromium == nil || !s.navDone.Load() {
		return
	}
	remixsid, pCookie := s.readVKCookies()

	// Login wall is often https://vk.ru/ with QR overlay — keep absorbing
	// guest remixsid into baseline and never finish until a post-login URL.
	if !vkLoginURLLooksLoggedIn(s.lastURL) {
		if remixsid != "" && remixsid != s.baselineRemixsid {
			vkLoginLog(s.dataDir, "auth-wall baseline %q → %q url=%q", s.baselineRemixsid, remixsid, s.lastURL)
			s.baselineRemixsid = remixsid
		}
		return
	}

	// On feed/im/…: accept session if remixsid changed OR same cookie validates
	// (VK may upgrade guest remixsid in place after QR success).
	if strings.TrimSpace(remixsid) == "" || strings.TrimSpace(pCookie) == "" {
		return
	}
	header := "remixsid=" + remixsid + "; p=" + pCookie
	if err := core.ValidateVKCookieHeader(header); err != nil {
		vkLoginLog(s.dataDir, "web_token not ready: %v url=%q", err, s.lastURL)
		return
	}
	if !vkRemixsidIsNew(remixsid, s.baselineRemixsid) {
		// Same value as wall baseline but URL is logged-in and token works — OK.
		vkLoginLog(s.dataDir, "logged-in url with stable remixsid url=%q", s.lastURL)
	}
	now := time.Now()
	if s.pendingHeader != header {
		s.pendingHeader = header
		s.pendingSince = now
		return
	}
	if now.Sub(s.pendingSince) < vkLoginPendingStable {
		return
	}
	s.done.Store(true)
	vkLoginLog(s.dataDir, "login ok remixsid=%s… url=%q", remixsid[:min(8, len(remixsid))], s.lastURL)
	s.writeSt(vkLoginStatusFile{Done: true, Status: "done", Message: "Cookies сохранены", Cookie: header})
}

func vkLoginLog(dataDir, format string, args ...any) {
	dir := filepath.Dir(dataDir)
	if dir == "" || dir == "." {
		dir = vkLoginDataDir()
	}
	logPath := filepath.Join(dir, "vk-login.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "[%s] ", time.Now().Format("15:04:05"))
	_, _ = fmt.Fprintf(f, format+"\n", args...)
}
