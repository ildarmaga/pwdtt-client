//go:build windows

package backend

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// DownloadAndApplyUpdate downloads the latest Windows exe and schedules replace+restart.
// Works only when VPN/WB tunnel is disconnected.
func (a *App) DownloadAndApplyUpdate() UpdateApplyResult {
	if a.orch.IsRunning() || a.wb.IsRunning() {
		return UpdateApplyResult{Message: "Сначала отключите VPN (кнопка питания)"}
	}

	info := a.CheckForUpdate()
	if info.Error != "" && info.DownloadURL == "" {
		return UpdateApplyResult{Message: info.Error}
	}
	if !info.HasUpdate {
		if info.Latest != "" {
			return UpdateApplyResult{Message: "Уже установлена " + info.Latest}
		}
		return UpdateApplyResult{Message: "Обновлений нет"}
	}
	url := strings.TrimSpace(info.DownloadURL)
	if url == "" {
		return UpdateApplyResult{Message: "Нет ссылки на скачивание"}
	}

	exePath, err := os.Executable()
	if err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}

	tmpDir := filepath.Join(os.TempDir(), "wdtt-update")
	if err := os.MkdirAll(tmpDir, 0700); err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}
	newExe := filepath.Join(tmpDir, "wdtt-new.exe")
	if err := downloadFile(url, newExe); err != nil {
		return UpdateApplyResult{Message: "Скачивание: " + err.Error()}
	}

	batPath := filepath.Join(tmpDir, "wdtt-apply.bat")
	bat := fmt.Sprintf(`@echo off
timeout /t 2 /nobreak >nul
copy /Y "%s" "%s" >nul
if errorlevel 1 exit /b 1
start "" "%s"
del "%s" >nul 2>&1
del "%%~f0"
`, newExe, exePath, exePath, newExe)
	if err := os.WriteFile(batPath, []byte(bat), 0600); err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}

	cmd := execHidden("cmd.exe", "/C", "start", "", batPath)
	if err := cmd.Start(); err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}

	a.quitting.Store(true)
	go func() {
		time.Sleep(400 * time.Millisecond)
		runtime.Quit(a.ctx)
	}()

	return UpdateApplyResult{
		OK:      true,
		Message: fmt.Sprintf("Обновление до %s — приложение перезапустится", info.Latest),
	}
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "WDTT-Desktop-updater")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 128<<20))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dest)
}
