//go:build !windows

package wbxray

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
