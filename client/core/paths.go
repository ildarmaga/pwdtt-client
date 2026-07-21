package core

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	configRootMu sync.RWMutex
	configRoot   string
)

// SetConfigRoot sets the portable config root (called once from the desktop app).
// Typical value: <dir-of-exe>/data
func SetConfigRoot(dir string) {
	dir = filepath.Clean(dir)
	configRootMu.Lock()
	configRoot = dir
	configRootMu.Unlock()
}

// ConfigRoot returns the active config directory.
// Before SetConfigRoot: legacy ~/.config/pwdtt (Linux) / %AppData%\pwdtt (Windows).
func ConfigRoot() string {
	configRootMu.RLock()
	root := configRoot
	configRootMu.RUnlock()
	if root != "" {
		return root
	}
	return legacyConfigRoot()
}

func legacyConfigRoot() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.Getenv("HOME")
	}
	return filepath.Join(base, "pwdtt")
}
