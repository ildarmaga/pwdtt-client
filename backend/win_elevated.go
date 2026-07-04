//go:build windows

package backend

import (
	"fmt"
	"os"
	"path/filepath"
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

// runDeElevated starts exePath at medium integrity via explorer.exe (no CreateProcessAsUser).
func runDeElevated(exePath string, args []string, workDir string) (uint32, error) {
	cmdLine := windowsCmdLine(exePath, args)
	vbsPath := filepath.Join(os.TempDir(), "wdtt-vk-launch.vbs")
	vbs := fmt.Sprintf(
		"CreateObject(\"WScript.Shell\").Run %s, 1, False\r\n",
		vbsQuote(cmdLine),
	)
	if err := os.WriteFile(vbsPath, []byte(vbs), 0600); err != nil {
		return 0, err
	}

	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	explorer := filepath.Join(systemRoot, "explorer.exe")

	cmd := execHidden(explorer, vbsPath)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("explorer launch: %w", err)
	}
	// Worker PID is written to status.json by the subprocess.
	return 0, nil
}

func vbsQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
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

// killProcessTree terminates pid and direct children (Edge/chromedp from VK worker).
func killProcessTree(pid uint32) {
	if pid == 0 {
		return
	}
	_ = execHidden("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
}
