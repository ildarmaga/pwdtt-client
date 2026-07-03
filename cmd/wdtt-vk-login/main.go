//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/internal/w32"
	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

const (
	vkLoginURL  = "https://vk.com/"
	timerPollID = 42
)

type statusFile struct {
	Done    bool   `json:"done"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Cookie  string `json:"cookie,omitempty"`
}

var (
	chromium  *edge.Chromium
	done      atomic.Bool
	statusOut string
)

func writeStatus(st statusFile) {
	if statusOut == "" {
		return
	}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(statusOut, b, 0600)
}

func tryHarvest() {
	if done.Load() || chromium == nil {
		return
	}
	cm, err := chromium.GetCookieManager()
	if err != nil {
		return
	}
	defer cm.Release()

	var remixsid, pCookie string
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
	if remixsid == "" {
		writeStatus(statusFile{Status: "waiting", Message: "Войдите в VK — ожидаем remixsid…"})
		return
	}
	header := "remixsid=" + remixsid
	if pCookie != "" {
		header += "; p=" + pCookie
	}
	done.Store(true)
	writeStatus(statusFile{Done: true, Status: "done", Message: "Cookies сохранены", Cookie: header})
}

func wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case w32.WMSize:
		if chromium != nil {
			chromium.Resize()
		}
	case 0x0113: // WM_TIMER
		if wParam == timerPollID {
			tryHarvest()
			if done.Load() {
				w32.User32DestroyWindow.Call(hwnd)
			}
		}
	case w32.WMDestroy:
		if !done.Load() {
			writeStatus(statusFile{Status: "cancelled", Message: "Вход отменён"})
		}
		w32.User32PostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

func main() {
	out := flag.String("status", "", "status json path")
	dataDir := flag.String("data", "", "WebView2 user data dir")
	flag.Parse()
	statusOut = *out
	writeStatus(statusFile{Status: "waiting", Message: "Загрузка VK…"})

	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil {
		writeStatus(statusFile{Status: "error", Message: err.Error()})
		os.Exit(1)
	}
	defer windows.CoUninitialize()

	var hinstance windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &hinstance)

	className, _ := windows.UTF16PtrFromString("WDTTVKLoginWnd")
	wc := w32.WndClassExW{
		CbSize:        uint32(unsafe.Sizeof(w32.WndClassExW{})),
		HInstance:     hinstance,
		LpszClassName: className,
		LpfnWndProc:   windows.NewCallback(wndProc),
	}
	if _, _, _ = w32.User32RegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); true {
	}

	title, _ := windows.UTF16PtrFromString("WDTT — вход VK")
	hwnd, _, _ := w32.User32CreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		w32.WSOverlappedWindow,
		uintptr(w32.CW_USEDEFAULT), uintptr(w32.CW_USEDEFAULT),
		520, 720,
		0, 0, uintptr(hinstance), 0,
	)
	if hwnd == 0 {
		writeStatus(statusFile{Status: "error", Message: "не удалось создать окно"})
		os.Exit(1)
	}

	chromium = edge.NewChromium()
	if *dataDir != "" {
		chromium.DataPath = *dataDir
	} else {
		chromium.DataPath = filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "profile")
	}
	_ = os.MkdirAll(chromium.DataPath, 0700)
	chromium.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		tryHarvest()
	}

	if !chromium.Embed(hwnd) {
		writeStatus(statusFile{Status: "error", Message: "WebView2 не установлен — установите Microsoft Edge WebView2 Runtime"})
		os.Exit(1)
	}

	w32.User32ShowWindow.Call(hwnd, w32.SWShow)
	w32.User32UpdateWindow.Call(hwnd)
	chromium.Resize()
	chromium.Navigate(vkLoginURL)

	setTimer := user32Proc("SetTimer")
	setTimer.Call(hwnd, timerPollID, 1200, 0)
	writeStatus(statusFile{Status: "waiting", Message: "Войдите в VK — cookies сохранятся автоматически"})

	var msg w32.Msg
	for {
		if done.Load() {
			time.Sleep(500 * time.Millisecond)
			w32.User32DestroyWindow.Call(hwnd)
		}
		r, _, _ := w32.User32GetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 {
			break
		}
		w32.User32TranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		w32.User32DispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func user32Proc(name string) *windows.LazyProc {
	return windows.NewLazySystemDLL("user32").NewProc(name)
}
