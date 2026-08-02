//go:build windows

package wbjrunner

import (
	"fmt"
	"syscall"
)

func tunPrecheck() error {
	h, err := syscall.LoadLibrary("wintun.dll")
	if err != nil {
		return fmt.Errorf("wintun.dll not loadable: %w", err)
	}
	_ = syscall.FreeLibrary(h)
	return nil
}
