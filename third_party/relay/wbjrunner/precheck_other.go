//go:build !windows

package wbjrunner

import (
	"fmt"
	"os"
)

func tunPrecheck() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("tun requires root privileges")
	}
	return nil
}
