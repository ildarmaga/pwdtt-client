//go:build windows

package backend

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// runDeElevated starts exePath at medium integrity via scheduled task (/RL LIMITED).
func runDeElevated(exePath string, args []string, workDir string) (uint32, error) {
	taskName := fmt.Sprintf("WDTT_VK_%d", time.Now().UnixNano())
	tr := windowsCmdLine(exePath, args)
	if workDir != "" {
		tr = fmt.Sprintf(`cmd /c "cd /d "%s" && %s"`, workDir, tr)
	}
	if err := runScheduledTask(taskName, tr, true); err != nil {
		return 0, err
	}
	go func() {
		time.Sleep(2 * time.Minute)
		_ = execHidden("schtasks", "/Delete", "/TN", taskName, "/F").Run()
	}()
	return 0, nil
}

// runScheduledTask creates a one-shot task and runs it immediately.
// limited=true → /RL LIMITED (medium integrity, for VK worker de-elevation).
func runScheduledTask(taskName, taskRun string, limited bool) error {
	_ = execHidden("schtasks", "/Delete", "/TN", taskName, "/F").Run()

	createArgs := []string{
		"/Create", "/TN", taskName,
		"/TR", taskRun,
		"/SC", "ONCE", "/ST", "00:00",
		"/F",
	}
	if limited {
		createArgs = append(createArgs, "/RL", "LIMITED")
	}
	if err := execHidden("schtasks", createArgs...).Run(); err != nil {
		return fmt.Errorf("schtasks create: %w", err)
	}
	if err := execHidden("schtasks", "/Run", "/TN", taskName).Run(); err != nil {
		_ = execHidden("schtasks", "/Delete", "/TN", taskName, "/F").Run()
		return fmt.Errorf("schtasks run: %w", err)
	}
	return nil
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

func killProcessTree(pid uint32) {
	if pid == 0 {
		return
	}
	_ = execHidden("taskkill", "/F", "/T", "/PID", strconv.Itoa(int(pid))).Run()
}
