//go:build !windows

package backend

import "os/exec"

// hideConsoleWindow — no-op вне Windows (на Linux/macOS отдельного окна нет).
func hideConsoleWindow(cmd *exec.Cmd) {}
