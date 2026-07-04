//go:build windows

package backend

import (
	"fmt"
	"strings"
	"syscall"
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

func runDeElevated(exePath string, args []string, workDir string) (uint32, error) {
	hwnd := windows.GetShellWindow()
	if hwnd == 0 {
		return 0, fmt.Errorf("GetShellWindow: окно оболочки не найдено")
	}

	var explorerPID uint32
	windows.GetWindowThreadProcessId(hwnd, &explorerPID)
	if explorerPID == 0 {
		return 0, fmt.Errorf("explorer.exe не найден")
	}

	hProc, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, explorerPID)
	if err != nil {
		return 0, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(hProc)

	var hToken windows.Token
	if err := windows.OpenProcessToken(hProc, windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY, &hToken); err != nil {
		return 0, fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer hToken.Close()

	var hDupToken windows.Token
	if err := windows.DuplicateTokenEx(
		hToken,
		windows.MAXIMUM_ALLOWED,
		nil,
		windows.SecurityImpersonation,
		windows.TokenPrimary,
		&hDupToken,
	); err != nil {
		return 0, fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer hDupToken.Close()

	cmdLine := windowsCmdLine(exePath, args)
	cmdLineUTF16, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		return 0, err
	}

	var workDirPtr *uint16
	if workDir != "" {
		workDirPtr, err = windows.UTF16PtrFromString(workDir)
		if err != nil {
			return 0, err
		}
	}

	si := &windows.StartupInfo{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	var pi windows.ProcessInformation

	if err := windows.CreateProcessAsUser(
		hDupToken,
		nil,
		cmdLineUTF16,
		nil,
		nil,
		false,
		windows.CREATE_UNICODE_ENVIRONMENT,
		nil,
		workDirPtr,
		si,
		&pi,
	); err != nil {
		return 0, fmt.Errorf("CreateProcessAsUser: %w", err)
	}

	windows.CloseHandle(pi.Thread)
	windows.CloseHandle(pi.Process)
	return pi.ProcessId, nil
}

func windowsCmdLine(exe string, args []string) string {
	var b strings.Builder
	b.WriteString(syscall.EscapeArg(exe))
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(syscall.EscapeArg(a))
	}
	return b.String()
}

func killProcess(pid uint32) error {
	if pid == 0 {
		return nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}
