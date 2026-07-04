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
	var errs []string
	if pid, err := runDeElevatedWTS(exePath, args, workDir); err == nil {
		return pid, nil
	} else {
		errs = append(errs, err.Error())
	}
	if pid, err := runDeElevatedExplorer(exePath, args, workDir); err == nil {
		return pid, nil
	} else {
		errs = append(errs, err.Error())
	}
	return 0, fmt.Errorf("de-elevation failed: %s", strings.Join(errs, "; "))
}

func runDeElevatedWTS(exePath string, args []string, workDir string) (uint32, error) {
	sessionID := windows.WTSGetActiveConsoleSessionId()
	if sessionID == 0xFFFFFFFF {
		return 0, fmt.Errorf("no active session")
	}

	var userToken windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &userToken); err != nil {
		return 0, fmt.Errorf("WTSQueryUserToken: %w", err)
	}
	defer userToken.Close()

	var primaryToken windows.Token
	if err := windows.DuplicateTokenEx(
		userToken,
		windows.MAXIMUM_ALLOWED,
		nil,
		windows.SecurityIdentification,
		windows.TokenPrimary,
		&primaryToken,
	); err != nil {
		return 0, fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer primaryToken.Close()

	return createProcessAsUserToken(primaryToken, exePath, args, workDir)
}

func runDeElevatedExplorer(exePath string, args []string, workDir string) (uint32, error) {
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

	return createProcessAsUserToken(hDupToken, exePath, args, workDir)
}

func createProcessAsUserToken(token windows.Token, exePath string, args []string, workDir string) (uint32, error) {
	_ = enableTokenPrivilege("SeIncreaseQuotaPrivilege")
	_ = enableTokenPrivilege("SeAssignPrimaryTokenPrivilege")

	var envBlock *uint16
	if err := windows.CreateEnvironmentBlock(&envBlock, token, false); err != nil {
		return 0, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(envBlock)

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
		token,
		nil,
		cmdLineUTF16,
		nil,
		nil,
		false,
		windows.CREATE_UNICODE_ENVIRONMENT,
		envBlock,
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

func enableTokenPrivilege(name string) error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()

	var luid windows.LUID
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	if err := windows.LookupPrivilegeValue(nil, namePtr, &luid); err != nil {
		return err
	}

	tp := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges: [1]windows.LUIDAndAttributes{
			{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED},
		},
	}
	return windows.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil)
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
