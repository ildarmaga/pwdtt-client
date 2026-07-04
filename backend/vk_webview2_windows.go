//go:build windows

package backend

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/lxn/win"
	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

const vkWebView2TimerID = 42

type vkWebView2Session struct {
	chromium *edge.Chromium
	done     atomic.Bool
	navDone  atomic.Bool
	// remixsid already in the WebView2 profile before this login attempt —
	// harvesting it would close the window in ~1s without showing vk.com.
	baselineRemixsid string
	writeSt          func(vkLoginStatusFile)
}

// runVKWebView2Window opens a native window with WebView2 pointed at vk.com and
// harvests remixsid/p cookies via the WebView2 cookie manager (works with HttpOnly,
// works elevated — no DevTools attach like chromedp). Blocks until the window closes.
func runVKWebView2Window(dataDir string, writeSt func(vkLoginStatusFile)) error {
	runtime.LockOSThread()

	s := &vkWebView2Session{writeSt: writeSt}

	// COM is already initialized (STA) on this thread by the go-webview2 edge
	// package init() when this exe starts, so we must NOT call CoInitializeEx
	// again here — a second call returns S_FALSE, which x/sys/windows surfaces
	// as the misleading "Incorrect function." error.

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
					win.DestroyWindow(hwnd)
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

	// Center on screen; the worker is a separate process, so plain
	// SetForegroundWindow may be denied — create the window TOPMOST so the
	// user always sees the login page instead of it hiding behind the app.
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
	_ = os.MkdirAll(dataDir, 0700)
	chromium.SetErrorCallback(func(err error) {
		writeSt(vkLoginStatusFile{Status: "error", Message: "WebView2: " + err.Error() + " — установите Microsoft Edge WebView2 Runtime"})
		win.PostQuitMessage(1)
	})
	chromium.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		if !s.navDone.Load() {
			s.navDone.Store(true)
			s.baselineRemixsid = s.readRemixsid()
		}
		s.tryHarvest()
	}
	s.chromium = chromium

	if !chromium.Embed(uintptr(hwnd)) {
		writeSt(vkLoginStatusFile{Status: "error", Message: "WebView2 не установлен — установите Microsoft Edge WebView2 Runtime"})
		return fmt.Errorf("webview2 embed failed")
	}
	if settings, err := chromium.GetSettings(); err == nil {
		_ = settings.PutAreDevToolsEnabled(false)
	}

	// First ShowWindow call in a process obeys STARTUPINFO wShowWindow, not the
	// argument — call twice and force with SWP_SHOWWINDOW so the window is
	// visible regardless of how the parent spawned us.
	win.ShowWindow(hwnd, win.SW_SHOWNORMAL)
	win.ShowWindow(hwnd, win.SW_SHOWNORMAL)
	win.UpdateWindow(hwnd)
	win.SetWindowPos(hwnd, win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_SHOWWINDOW)
	win.SetForegroundWindow(hwnd)
	chromium.Resize()
	chromium.Navigate("https://vk.com/")

	win.SetTimer(hwnd, vkWebView2TimerID, 1200, 0)
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

	for _, uri := range []string{"https://vk.com/", "https://login.vk.com/", "https://id.vk.com/"} {
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
			dom = strings.ToLower(dom)
			if !strings.HasPrefix(dom, ".") {
				dom = "." + dom
			}
			if name == "remixsid" && strings.HasSuffix(dom, ".vk.com") && val != "" {
				remixsid = val
			}
			if name == "p" && strings.HasSuffix(dom, ".login.vk.com") && val != "" {
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
	if !vkRemixsidIsNew(remixsid, s.baselineRemixsid) {
		return
	}
	header := "remixsid=" + remixsid
	if pCookie != "" {
		header += "; p=" + pCookie
	}
	s.done.Store(true)
	s.writeSt(vkLoginStatusFile{Done: true, Status: "done", Message: "Cookies сохранены", Cookie: header})
}
