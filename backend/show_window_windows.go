//go:build windows

package backend

import (
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

func bringMainWindowToFront() {
	focusWailsMainWindow()
}

func focusWailsMainWindow() {
	title, _ := windows.UTF16PtrFromString("WDTT")
	hwnd := win.FindWindow(nil, title)
	if hwnd == 0 {
		return
	}
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	win.SetForegroundWindow(hwnd)
}
