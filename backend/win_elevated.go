//go:build windows

package backend

import (
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

func isProcessElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()

	var elev uint32
	var outLen uint32
	err := windows.GetTokenInformation(
		token,
		windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elev)),
		uint32(unsafe.Sizeof(elev)),
		&outLen,
	)
	if err != nil {
		return false
	}
	return elev != 0
}

func killProcessTree(pid uint32) {
	if pid == 0 {
		return
	}
	_ = execHidden("taskkill", "/F", "/T", "/PID", strconv.Itoa(int(pid))).Run()
}
