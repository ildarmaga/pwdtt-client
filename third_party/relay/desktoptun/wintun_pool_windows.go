//go:build windows

package desktoptun

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"golang.zx2c4.com/wintun"
)

const errAlreadyExists = syscall.Errno(0x800700B7)

// ReleaseWintunPool closes an orphaned wintun kernel adapter by name.
func ReleaseWintunPool(name string) error {
	_, err := releaseWintunPoolOnce(name)
	return err
}

func releaseWintunPoolOnce(name string) (closed bool, err error) {
	if name == "" {
		return false, nil
	}
	a, err := wintun.OpenAdapter(name)
	if err != nil {
		return false, err
	}
	if err := a.Close(); err != nil {
		return false, fmt.Errorf("wintun close %q: %w", name, err)
	}
	return true, nil
}

// QuickReleaseWintunPool is the fast connect path (~300ms): close pool if open.
func QuickReleaseWintunPool(name string) error {
	for i := 0; i < 2; i++ {
		if closed, _ := releaseWintunPoolOnce(name); closed {
			time.Sleep(100 * time.Millisecond)
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}

// ForceReleaseWintunPool retries pool release with netdev cleanup (retry / teardown).
func ForceReleaseWintunPool(name string) error {
	var lastOpen error
	for i := 0; i < 3; i++ {
		if closed, err := releaseWintunPoolOnce(name); closed {
			time.Sleep(200 * time.Millisecond)
			return nil
		} else if err != nil {
			lastOpen = err
		}
		deepCleanupLegacyAdapters()
		time.Sleep(250 * time.Millisecond)
	}
	if lastOpen != nil {
		return fmt.Errorf("wintun open %q: %w", name, lastOpen)
	}
	return nil
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	if err == errAlreadyExists {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "0x800700b7") ||
		strings.Contains(msg, "уже существует")
}

func listLegacyWintunPoolNames() []string {
	out, err := runHidden("powershell", "-NoProfile", "-Command",
		`Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object { $_.Name -like 'WDTT-WB*' } | Select-Object -ExpandProperty Name`)
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}
