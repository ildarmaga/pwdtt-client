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
	vkWebView2TimerID    = 42
	vkLoginPendingStable = 3 * time.Second
)

type vkWebView2Session struct {
	chromium *edge.Chromium
	hwnd     win.HWND
	ready    atomic.Bool // controller embedded; safe to Resize/Navigate
	busy     atomic.Bool // harvest in progress (GetCookies pumps messages)
	done     atomic.Bool
	navDone  atomic.Bool
	// remixsid seen while still on the login wall — must not finish harvest.
	baselineRemixsid string
	firstNavAt       time.Time
	lastURL          string
	pendingHeader    string
	pendingSince     time.Time
	writeSt          func(vkLoginStatusFile)
	dataDir          string
	wndProcCb        uintptr // keep NewCallback alive for process lifetime
}

// runVKWebView2Window opens a native window with WebView2 pointed at vk.ru and
// harvests remixsid/p cookies via the WebView2 cookie manager.
//
// The window NEVER auto-closes: not on harvest success, not on WebView2
// ProcessFailed, not on COM warnings. Only the user closing the window
// (WM_DESTROY) ends the message loop.
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
				s.tryHarvest()
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
	atom := win.RegisterClassEx(&wc)
	if atom == 0 {
		// Class may already exist from a previous attempt in this process — OK.
		vkLoginLog(dataDir, "RegisterClassEx returned 0 (class may already exist)")
	}

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
	chromium.AdditionalBrowserArgs = []string{
		"--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection",
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
		vkLoginLog(dataDir, "webview2 process failed kind=%d — ignored (no quit, no reload)", kind)
		writeSt(vkLoginStatusFile{Status: "waiting", Message: "WebView2 сбой процесса — закройте окно и войдите снова, если страница пустая"})
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
	}
	// Do NOT Hide()/SetBackgroundColour — both historically nil-deref'd on
	// some WebView2 builds (Controller2 missing) and killed this process.
	if err := chromium.Show(); err != nil {
		vkLoginLog(dataDir, "Show warn: %v", err)
	}

	win.ShowWindow(hwnd, win.SW_SHOWNORMAL)
	win.UpdateWindow(hwnd)
	win.SetForegroundWindow(hwnd)
	chromium.Resize()
	vkLoginLog(dataDir, "Navigate https://vk.ru/")
	chromium.Navigate("https://vk.ru/")

	win.SetTimer(hwnd, vkWebView2TimerID, 1500, 0)
	writeSt(vkLoginStatusFile{Status: "waiting", Message: "Войдите в VK — cookies сохранятся автоматически. Окно закройте сами (крестик)."})
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

func (s *vkWebView2Session) readRemixsid() string {
	remixsid, _ := s.readVKCookies()
	return remixsid
}

func (s *vkWebView2Session) readVKCookies() (remixsid, pCookie string) {
	if s.chromium == nil || !s.ready.Load() {
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
	defer func() {
		if r := recover(); r != nil {
			vkLoginLog(s.dataDir, "tryHarvest panic: %v\n%s", r, debug.Stack())
		}
	}()
	if s.done.Load() || s.chromium == nil || !s.navDone.Load() {
		return
	}
	// GetCookies nests a message pump; ignore re-entrant timer/nav callbacks.
	if !s.busy.CompareAndSwap(false, true) {
		return
	}
	defer s.busy.Store(false)

	remixsid, pCookie := s.readVKCookies()

	if !vkLoginURLLooksLoggedIn(s.lastURL) {
		if remixsid != "" && remixsid != s.baselineRemixsid {
			vkLoginLog(s.dataDir, "auth-wall baseline %q → %q url=%q", s.baselineRemixsid, remixsid, s.lastURL)
			s.baselineRemixsid = remixsid
		}
		return
	}

	if strings.TrimSpace(remixsid) == "" || strings.TrimSpace(pCookie) == "" {
		return
	}
	header := "remixsid=" + remixsid + "; p=" + pCookie
	if err := core.ValidateVKCookieHeader(header); err != nil {
		vkLoginLog(s.dataDir, "web_token not ready: %v url=%q", err, s.lastURL)
		return
	}
	if !vkRemixsidIsNew(remixsid, s.baselineRemixsid) {
		vkLoginLog(s.dataDir, "skip harvest: remixsid still baseline url=%q", s.lastURL)
		return
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
	vkLoginLog(s.dataDir, "login ok remixsid=%s… url=%q (window stays open until user closes)", remixsid[:min(8, len(remixsid))], s.lastURL)
	s.writeSt(vkLoginStatusFile{Done: true, Status: "done", Message: "Cookies сохранены — можете закрыть окно", Cookie: header})
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
