//go:build windows

package backend

import (
	"fmt"
	"os"
	"path/filepath"
)

var (
	xrayEXE       []byte
	geoipDAT      []byte
	wintunXrayDLL []byte
	geositeDAT    []byte
)

// InitXray stores embedded xray-core assets for WB VPN routing.
func InitXray(exe, geoip, wintun, geosite []byte) {
	xrayEXE = exe
	geoipDAT = geoip
	wintunXrayDLL = wintun
	geositeDAT = geosite
}

// prepareWBXray writes xray.exe, wintun.dll and geoip.dat into <exe>/xray/.
func prepareWBXray() error {
	if len(xrayEXE) == 0 {
		return fmt.Errorf("xray.exe не встроен — пересоберите с scripts/fetch-xray-assets.sh")
	}
	if len(wintunXrayDLL) == 0 {
		return fmt.Errorf("wintun.dll для xray не встроен — пересоберите с scripts/fetch-xray-assets.sh")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Join(filepath.Dir(exe), "xray")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := writeIfDifferent(filepath.Join(dir, "xray.exe"), xrayEXE, 0755); err != nil {
		return fmt.Errorf("extract xray.exe: %w", err)
	}
	if err := writeIfDifferent(filepath.Join(dir, "wintun.dll"), wintunXrayDLL, 0644); err != nil {
		return fmt.Errorf("extract wintun.dll: %w", err)
	}
	if len(geoipDAT) > 0 {
		if err := writeIfDifferent(filepath.Join(dir, "geoip.dat"), geoipDAT, 0644); err != nil {
			return fmt.Errorf("extract geoip.dat: %w", err)
		}
	}
	if len(geositeDAT) > 0 {
		if err := writeIfDifferent(filepath.Join(dir, "geosite.dat"), geositeDAT, 0644); err != nil {
			return fmt.Errorf("extract geosite.dat: %w", err)
		}
	}
	return nil
}

func writeIfDifferent(path string, data []byte, mode os.FileMode) error {
	if fi, err := os.Stat(path); err == nil && fi.Size() == int64(len(data)) {
		return nil
	}
	return os.WriteFile(path, data, mode)
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
