//go:build windows

package backend

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// IsTunnelRunning reports whether VK or WB tunnel is active.
func (a *App) IsTunnelRunning() bool {
	return a.orch.IsRunning() || a.wb.IsRunning()
}

// DownloadAndApplyUpdate downloads the latest Windows exe and schedules replace+restart.
func (a *App) DownloadAndApplyUpdate() UpdateApplyResult {
	if a.IsTunnelRunning() {
		return UpdateApplyResult{Message: "Сначала отключитесь — нажмите кнопку питания на главном экране"}
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
	if err := a.downloadFileWithProgress(url, newExe); err != nil {
		a.emitUpdateProgress(UpdateProgress{Phase: "error", Message: err.Error()})
		return UpdateApplyResult{Message: "Скачивание: " + err.Error()}
	}
	if err := verifyWindowsExe(newExe); err != nil {
		a.emitUpdateProgress(UpdateProgress{Phase: "error", Message: err.Error()})
		return UpdateApplyResult{Message: "Скачанный файл повреждён: " + err.Error()}
	}

	a.emitUpdateProgress(UpdateProgress{Phase: "applying", Percent: 100, Message: "Установка…"})

	logPath := filepath.Join(tmpDir, "apply.log")
	batPath := filepath.Join(tmpDir, "wdtt-apply.bat")
	bat := updateApplyBatchScript()
	if err := os.WriteFile(batPath, []byte(bat), 0600); err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}

	pid := os.Getpid()
	cmd := execHidden("cmd.exe", "/c", batPath,
		strconv.Itoa(pid),
		newExe,
		exePath,
		logPath,
	)
	if err := cmd.Start(); err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}

	a.quitting.Store(true)
	a.orch.Stop()
	a.wb.Disconnect()
	go func() {
		time.Sleep(500 * time.Millisecond)
		runtime.Quit(a.ctx)
	}()

	return UpdateApplyResult{
		OK:      true,
		Message: fmt.Sprintf("Обновление до %s — приложение перезапустится", info.Latest),
	}
}

func updateApplyBatchScript() string {
	return "@echo off\r\n" +
		"setlocal EnableExtensions\r\n" +
		"set \"PID=%~1\"\r\n" +
		"set \"NEW=%~2\"\r\n" +
		"set \"DEST=%~3\"\r\n" +
		"set \"LOG=%~4\"\r\n" +
		"echo [%date% %time%] update start pid=%PID% >> \"%LOG%\"\r\n" +
		":waitproc\r\n" +
		"tasklist /FI \"PID eq %PID%\" 2>nul | find /I \"%PID%\" >nul\r\n" +
		"if not errorlevel 1 (\r\n" +
		"  timeout /t 1 /nobreak >nul\r\n" +
		"  goto waitproc\r\n" +
		")\r\n" +
		"echo [%date% %time%] process exited >> \"%LOG%\"\r\n" +
		"set /a N=0\r\n" +
		":trycopy\r\n" +
		"set /a N+=1\r\n" +
		"copy /Y \"%NEW%\" \"%DEST%\" >> \"%LOG%\" 2>&1\r\n" +
		"if errorlevel 1 (\r\n" +
		"  if %N% lss 60 (\r\n" +
		"    timeout /t 1 /nobreak >nul\r\n" +
		"    goto trycopy\r\n" +
		"  )\r\n" +
		"  echo COPY FAILED after %N% tries >> \"%LOG%\"\r\n" +
		"  exit /b 1\r\n" +
		")\r\n" +
		"echo [%date% %time%] copy ok >> \"%LOG%\"\r\n" +
		"start \"\" \"%DEST%\"\r\n" +
		"echo [%date% %time%] restarted >> \"%LOG%\"\r\n" +
		"exit /b 0\r\n"
}

func verifyWindowsExe(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() < 8<<20 {
		return fmt.Errorf("слишком маленький файл (%d байт)", fi.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return err
	}
	if hdr[0] != 'M' || hdr[1] != 'Z' {
		return fmt.Errorf("не PE-файл")
	}
	return nil
}

func (a *App) emitUpdateProgress(p UpdateProgress) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "update_progress", p)
}

func (a *App) downloadFileWithProgress(url, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "WDTT-Desktop-updater")

	a.emitUpdateProgress(UpdateProgress{Phase: "downloading", Percent: 0, Message: "Скачивание…"})

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	buf := make([]byte, 32*1024)
	var written int64
	lastEmit := time.Now()

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				_ = f.Close()
				_ = os.Remove(tmp)
				return wErr
			}
			written += int64(n)
			if time.Since(lastEmit) > 200*time.Millisecond || (total > 0 && written >= total) {
				pct := 0
				if total > 0 {
					pct = int(written * 100 / total)
				}
				a.emitUpdateProgress(UpdateProgress{
					Phase:   "downloading",
					Percent: pct,
					Written: written,
					Total:   total,
					Message: fmt.Sprintf("Скачивание… %d%%", pct),
				})
				lastEmit = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return readErr
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
