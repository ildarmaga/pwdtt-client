//go:build windows

package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
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
	vkWebView2TimerID = 42
	// Slow poll: aggressive GetCookies+web_token on the UI thread froze the
	// page so the post-QR confirmation code never appeared.
	vkLoginTimerMS = 2500
)

type vkWebView2Session struct {
	chromium *edge.Chromium
	hwnd     win.HWND
	ready    atomic.Bool
	done     atomic.Bool
	navDone  atomic.Bool

	fetching   atomic.Bool // GetCookiesAsync in flight
	validating atomic.Bool // background web_token check
	baselineOK atomic.Bool

	baselineRemixsid string
	lastURL          string
	writeSt          func(vkLoginStatusFile)
	dataDir          string
	wndProcCb        uintptr
	cookieKeepAlive  atomic.Value // any — roots GetCookiesAsync handler

	// Set by validate goroutine; applied on UI timer tick.
	validateOK     atomic.Bool
	validateFail   atomic.Bool
	validateHeader atomic.Value // string
	validateErr    atomic.Value // string
}

// runVKWebView2Window opens a native window with WebView2 pointed at vk.ru and
// harvests remixsid/p cookies via the WebView2 cookie manager.
func runVKWebView2Window(dataDir string, writeSt func(vkLoginStatusFile)) (err error) {
	runtime.LockOSThread()

	defer func() {
		if r := recover(); r != nil {
			vkLoginLog(dataDir, "FATAL panic: %v\n%s", r, debug.Stack())
			writeSt(vkLoginStatusFile{Status: "error", Message: fmt.Sprintf("сбой окна VK: %v", r)})
			err = fmt.Errorf("vk webview panic: %v", r)
		}
	}()

	s := &vkWebView2Session{writeSt: writeSt, dataDir: dataDir}

	hInstance := win.GetModuleHandle(nil)
	className, _ := windows.UTF16PtrFromString("WDTTVKLoginWnd")

	wndProc := func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		defer func() {
			if r := recover(); r != nil {
				vkLoginLog(dataDir, "wndProc panic msg=%d: %v\n%s", msg, r, debug.Stack())
			}
		}()
		switch msg {
		case win.WM_SIZE:
			if s.ready.Load() && s.chromium != nil {
				s.chromium.Resize()
			}
		case win.WM_TIMER:
			if wParam == vkWebView2TimerID {
				s.onTimer()
			}
		case win.WM_CLOSE:
			win.DestroyWindow(hwnd)
			return 0
		case win.WM_DESTROY:
			if !s.done.Load() {
				writeSt(vkLoginStatusFile{Status: "cancelled", Message: "Вход отменён"})
			}
			win.PostQuitMessage(0)
			return 0
		}
		return win.DefWindowProc(hwnd, msg, wParam, lParam)
	}

	s.wndProcCb = windows.NewCallback(wndProc)
	wc := win.WNDCLASSEX{
		HInstance:     hInstance,
		LpszClassName: className,
		LpfnWndProc:   s.wndProcCb,
		HCursor:       win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW)),
		HbrBackground: win.HBRUSH(win.COLOR_WINDOW + 1),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if win.RegisterClassEx(&wc) == 0 {
		vkLoginLog(dataDir, "RegisterClassEx returned 0 (class may already exist)")
	}

	const winW, winH = 560, 780
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
		0, className, title,
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
	// Keep QR long-poll / WebSocket alive; avoid Edge tracking features that
	// break id.vk ↔ vk.ru cookie/storage for the confirmation-code step.
	chromium.AdditionalBrowserArgs = []string{
		"--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection,ThirdPartyStoragePartitioning,TrackingPrevention",
	}
	_ = os.MkdirAll(dataDir, 0700)
	vkLoginLog(dataDir, "worker start profile=%s pid=%d", dataDir, os.Getpid())
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
		vkLoginLog(dataDir, "webview2 process failed kind=%d — ignored", kind)
		writeSt(vkLoginStatusFile{Status: "waiting", Message: "WebView2 сбой — закройте окно и войдите снова"})
	}
	chromium.NavigationCompletedCallback = func(sender *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		defer func() {
			if r := recover(); r != nil {
				vkLoginLog(dataDir, "NavigationCompleted panic: %v\n%s", r, debug.Stack())
			}
		}()
		if sender != nil {
			if src, err := sender.GetSource(); err == nil && src != "" {
				s.lastURL = src
				vkLoginLog(dataDir, "nav url=%q", src)
			}
		}
		if !s.navDone.Load() {
			s.navDone.Store(true)
			vkLoginLog(dataDir, "first navigation done url=%q (baseline via async cookies)", s.lastURL)
		}
		// Do NOT GetCookies / web_token here — that froze the UI and blocked
		// the QR→confirmation-code transition after the phone scan.
	}
	s.chromium = chromium
	s.hwnd = hwnd

	vkLoginLog(dataDir, "Embed begin")
	if !chromium.Embed(uintptr(hwnd)) {
		vkLoginLog(dataDir, "Embed failed")
		writeSt(vkLoginStatusFile{Status: "error", Message: "WebView2 не установлен — установите Microsoft Edge WebView2 Runtime"})
		return fmt.Errorf("webview2 embed failed")
	}
	vkLoginLog(dataDir, "Embed ok")
	s.ready.Store(true)

	if settings, err := chromium.GetSettings(); err == nil && settings != nil {
		_ = settings.PutAreDevToolsEnabled(false)
		_ = settings.PutUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0")
	}
	if err := chromium.Show(); err != nil {
		vkLoginLog(dataDir, "Show warn: %v", err)
	}

	win.ShowWindow(hwnd, win.SW_SHOWNORMAL)
	win.UpdateWindow(hwnd)
	win.SetForegroundWindow(hwnd)
	chromium.Resize()
	vkLoginLog(dataDir, "Navigate https://vk.ru/")
	chromium.Navigate("https://vk.ru/")

	win.SetTimer(hwnd, vkWebView2TimerID, vkLoginTimerMS, 0)
	writeSt(vkLoginStatusFile{Status: "waiting", Message: "Отсканируйте QR — в этом окне появится код, подтвердите вход здесь"})
	vkLoginLog(dataDir, "message loop enter")

	var msg win.MSG
	for win.GetMessage(&msg, 0, 0, 0) > 0 {
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}
	vkLoginLog(dataDir, "message loop exit")
	runtime.KeepAlive(s)
	return nil
}

func (s *vkWebView2Session) onTimer() {
	defer func() {
		if r := recover(); r != nil {
			vkLoginLog(s.dataDir, "onTimer panic: %v\n%s", r, debug.Stack())
		}
	}()
	if s.done.Load() || !s.ready.Load() || !s.navDone.Load() {
		return
	}
	s.applyValidateResult()
	s.requestCookiesAsync()
}

func (s *vkWebView2Session) applyValidateResult() {
	if s.validateOK.CompareAndSwap(true, false) {
		header, _ := s.validateHeader.Load().(string)
		if header == "" || s.done.Load() {
			return
		}
		s.done.Store(true)
		vkLoginLog(s.dataDir, "login ok — writing status then closing")
		s.writeSt(vkLoginStatusFile{Done: true, Status: "done", Message: "Cookies сохранены", Cookie: header})
		// Brief pause so parent can observe status.json before the process exits.
		time.Sleep(300 * time.Millisecond)
		if s.hwnd != 0 {
			win.DestroyWindow(s.hwnd)
		}
		return
	}
	if s.validateFail.CompareAndSwap(true, false) {
		errStr, _ := s.validateErr.Load().(string)
		vkLoginLog(s.dataDir, "web_token not ready: %s url=%q", errStr, s.lastURL)
		s.validating.Store(false)
	}
}

func (s *vkWebView2Session) requestCookiesAsync() {
	if s.done.Load() || s.validating.Load() {
		return
	}
	if !s.fetching.CompareAndSwap(false, true) {
		return
	}
	if s.chromium == nil {
		s.fetching.Store(false)
		return
	}
	cm, err := s.chromium.GetCookieManager()
	if err != nil {
		s.fetching.Store(false)
		vkLoginLog(s.dataDir, "GetCookieManager: %v", err)
		return
	}
	// Empty URI = all profile cookies (one async round-trip).
	keep, err := cm.GetCookiesAsync("", func(list *edge.ICoreWebView2CookieList, err error) {
		defer cm.Release()
		defer s.fetching.Store(false)
		if err != nil {
			vkLoginLog(s.dataDir, "GetCookiesAsync: %v", err)
			return
		}
		remixsid, pCookie := parseVKCookieList(list)
		if list != nil {
			list.Release()
		}
		s.onCookies(remixsid, pCookie)
	})
	if err != nil {
		cm.Release()
		s.fetching.Store(false)
		vkLoginLog(s.dataDir, "GetCookiesAsync start: %v", err)
		return
	}
	s.cookieKeepAlive.Store(keep)
}

func parseVKCookieList(list *edge.ICoreWebView2CookieList) (remixsid, pCookie string) {
	if list == nil {
		return "", ""
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
		if name == "p" && val != "" && (vkCookieDomainOK(dom, ".login.vk.com", ".login.vk.ru") ||
			vkCookieDomainOK(dom, ".vk.com", ".vk.ru")) {
			pCookie = val
		}
		c.Release()
	}
	return remixsid, pCookie
}

func (s *vkWebView2Session) onCookies(remixsid, pCookie string) {
	if s.done.Load() {
		return
	}
	if !s.baselineOK.Load() {
		s.baselineRemixsid = remixsid
		s.baselineOK.Store(true)
		vkLoginLog(s.dataDir, "baseline remixsid=%q p=%t url=%q", trimSID(remixsid), pCookie != "", s.lastURL)
		return
	}
	if s.chromium != nil {
		// Refresh URL if possible via last NavigationCompleted; leave as-is.
	}
	if !vkLoginURLAllowsCookieHarvest(s.lastURL) {
		return
	}
	if !vkRemixsidIsNew(remixsid, s.baselineRemixsid) {
		return
	}
	if !s.validating.CompareAndSwap(false, true) {
		return
	}
	header := "remixsid=" + remixsid
	if strings.TrimSpace(pCookie) != "" {
		header += "; p=" + pCookie
	}
	vkLoginLog(s.dataDir, "new remixsid — validate off UI thread url=%q p=%t", s.lastURL, pCookie != "")
	go s.validateHeaderAsync(header)
}

func (s *vkWebView2Session) validateHeaderAsync(header string) {
	// Off UI thread — never call web_token from the WebView message loop
	// (that froze QR SPA and prevented the confirmation-code screen).
	err := core.ValidateVKCookieHeader(header)
	if err != nil {
		s.validateErr.Store(err.Error())
		s.validateFail.Store(true)
		return
	}
	s.validateHeader.Store(header)
	s.validateOK.Store(true)
}

func trimSID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8] + "…"
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
	_, _ = fmt.Fprintf(f, "[%s] ", time.Now().Format("15:04:05.000"))
	_, _ = fmt.Fprintf(f, format+"\n", args...)
	_ = f.Sync()
}
