//go:build windows

package backend

import (
	"fmt"
	"os"
	"path/filepath"
)

var (
	xrayEXE   []byte
	geoipDAT  []byte
)

// InitXray stores embedded xray-core and geoip.dat for WB VPN routing.
func InitXray(exe, geoip []byte) {
	xrayEXE = exe
	geoipDAT = geoip
}

// prepareWBXray writes xray.exe and geoip.dat next to the app exe.
func prepareWBXray() error {
	if len(xrayEXE) == 0 {
		return fmt.Errorf("xray.exe не встроен — пересоберите с scripts/fetch-xray-assets.sh")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Join(filepath.Dir(exe), "xray")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	xrayPath := filepath.Join(dir, "xray.exe")
	if fi, err := os.Stat(xrayPath); err == nil && fi.Size() == int64(len(xrayEXE)) {
		// ok
	} else if err := os.WriteFile(xrayPath, xrayEXE, 0755); err != nil {
		return fmt.Errorf("extract xray.exe: %w", err)
	}
	if len(geoipDAT) > 0 {
		geoPath := filepath.Join(dir, "geoip.dat")
		if fi, err := os.Stat(geoPath); err != nil || fi.Size() != int64(len(geoipDAT)) {
			if err := os.WriteFile(geoPath, geoipDAT, 0644); err != nil {
				return fmt.Errorf("extract geoip.dat: %w", err)
			}
		}
	}
	return nil
}

func xrayBinaryPath() (string, error) {
	if err := prepareWBXray(); err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "xray", "xray.exe"), nil
}
