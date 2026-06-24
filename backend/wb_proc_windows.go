//go:build windows

package backend

import (
	"os/exec"
	"syscall"
)

// hideConsoleWindow прячет окно консоли дочернего процесса (wbt-joiner),
// чтобы он не всплывал отдельным чёрным окном поверх приложения.
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
