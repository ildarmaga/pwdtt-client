//go:build windows

package backend

import (
	"os/exec"
	"syscall"
)

const winCreateNoWindow = 0x08000000

func execHidden(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winCreateNoWindow,
		HideWindow:    true,
	}
	return cmd
}
