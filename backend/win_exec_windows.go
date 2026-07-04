//go:build windows

package backend

import (
	"os/exec"
	"strings"
	"syscall"
)

const (
	winCreateNoWindow        = 0x08000000
	winDetachedProcess       = 0x00000008
	winCreateNewProcessGroup = 0x00000200
	winCreateBreakawayFromJob = 0x01000000
)

func execHidden(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winCreateNoWindow,
		HideWindow:    true,
	}
	return cmd
}

// execDetached starts a process that survives parent exit (self-update helper).
func execDetached(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winCreateNoWindow | winDetachedProcess | winCreateNewProcessGroup | winCreateBreakawayFromJob,
		HideWindow:    true,
	}
	return cmd
}

// vbsQuote returns a VBScript string literal for WScript.Shell.Run.
func vbsQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
