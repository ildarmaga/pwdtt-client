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

const updateTaskName = "WDTT_ApplyUpdate"

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

	updateDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "WDTT", "update")
	if err := os.MkdirAll(updateDir, 0700); err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}
	newExe := filepath.Join(updateDir, "wdtt-new.exe")
	if err := a.downloadFileWithProgress(url, newExe); err != nil {
		a.emitUpdateProgress(UpdateProgress{Phase: "error", Message: err.Error()})
		return UpdateApplyResult{Message: "Скачивание: " + err.Error()}
	}
	if err := verifyWindowsExe(newExe); err != nil {
		a.emitUpdateProgress(UpdateProgress{Phase: "error", Message: err.Error()})
		return UpdateApplyResult{Message: "Скачанный файл повреждён: " + err.Error()}
	}

	a.emitUpdateProgress(UpdateProgress{Phase: "applying", Percent: 100, Message: "Установка…"})

	logPath := filepath.Join(updateDir, "apply.log")
	scriptPath := filepath.Join(updateDir, "apply.cmd")
	if err := os.WriteFile(scriptPath, []byte(buildUpdateScript(os.Getpid(), newExe, exePath, logPath, updateTaskName)), 0600); err != nil {
		return UpdateApplyResult{Message: err.Error()}
	}

	if err := runScheduledTask(updateTaskName, `"`+scriptPath+`"`, false); err != nil {
		return UpdateApplyResult{Message: "Не удалось запустить обновление: " + err.Error()}
	}

	a.quitting.Store(true)
	a.orch.Stop()
	a.wb.Disconnect()
	time.Sleep(200 * time.Millisecond)
	os.Exit(0)

	return UpdateApplyResult{OK: true, Message: fmt.Sprintf("Обновление до %s", info.Latest)}
}

func buildUpdateScript(pid int, newExe, destExe, logPath, taskName string) string {
	newSize := int64(0)
	if fi, err := os.Stat(newExe); err == nil {
		newSize = fi.Size()
	}
	return "@echo off\r\n" +
		"setlocal EnableExtensions\r\n" +
		"title WDTT Update\r\n" +
		"set \"PID=" + strconv.Itoa(pid) + "\"\r\n" +
		"set \"NEW=" + destExeEscape(newExe) + "\"\r\n" +
		"set \"DEST=" + destExeEscape(destExe) + "\"\r\n" +
		"set \"LOG=" + destExeEscape(logPath) + "\"\r\n" +
		"set \"NEWSIZE=" + strconv.FormatInt(newSize, 10) + "\"\r\n" +
		"set \"TASK=" + taskName + "\"\r\n" +
		"echo [%date% %time%] start pid=%PID% >> \"%LOG%\"\r\n" +
		"echo WDTT: closing, please wait...\r\n" +
		":waitproc\r\n" +
		"tasklist /FI \"PID eq %PID%\" 2>nul | find \"%PID%\" >nul\r\n" +
		"if not errorlevel 1 (\r\n" +
		"  ping 127.0.0.1 -n 2 >nul\r\n" +
		"  goto waitproc\r\n" +
		")\r\n" +
		"echo [%date% %time%] process exited >> \"%LOG%\"\r\n" +
		"ping 127.0.0.1 -n 3 >nul\r\n" +
		"set /a N=0\r\n" +
		":trycopy\r\n" +
		"set /a N+=1\r\n" +
		"if exist \"%DEST%.old\" del /F /Q \"%DEST%.old\" >nul 2>&1\r\n" +
		"if exist \"%DEST%\" move /Y \"%DEST%\" \"%DEST%.old\" >> \"%LOG%\" 2>&1\r\n" +
		"copy /Y \"%NEW%\" \"%DEST%\" >> \"%LOG%\" 2>&1\r\n" +
		"if errorlevel 1 goto copyretry\r\n" +
		"for %%A in (\"%DEST%\") do set \"DSize=%%~zA\"\r\n" +
		"if not \"%DSize%\"==\"%NEWSIZE%\" goto copyretry\r\n" +
		"if exist \"%DEST%.old\" del /F /Q \"%DEST%.old\" >nul 2>&1\r\n" +
		"echo [%date% %time%] copy ok size=%DSize% >> \"%LOG%\"\r\n" +
		"goto restart\r\n" +
		":copyretry\r\n" +
		"if exist \"%DEST%.old\" if not exist \"%DEST%\" move /Y \"%DEST%.old\" \"%DEST%\" >nul 2>&1\r\n" +
		"if %N% geq 120 goto copyfail\r\n" +
		"ping 127.0.0.1 -n 2 >nul\r\n" +
		"goto trycopy\r\n" +
		":copyfail\r\n" +
		"echo COPY FAILED >> \"%LOG%\"\r\n" +
		"echo Update FAILED. See %LOG%\r\n" +
		"pause\r\n" +
		"schtasks /Delete /TN \"%TASK%\" /F >nul 2>&1\r\n" +
		"exit /b 1\r\n" +
		":restart\r\n" +
		"echo WDTT: starting — confirm UAC if asked...\r\n" +
		"echo [%date% %time%] restart >> \"%LOG%\"\r\n" +
		"powershell -NoProfile -WindowStyle Normal -ExecutionPolicy Bypass -Command \"Start-Process -FilePath '%DEST%' -Verb RunAs\" >> \"%LOG%\" 2>&1\r\n" +
		"ping 127.0.0.1 -n 2 >nul\r\n" +
		"%SystemRoot%\\explorer.exe \"%DEST%\"\r\n" +
		"schtasks /Delete /TN \"%TASK%\" /F >nul 2>&1\r\n" +
		"exit /b 0\r\n"
}

func destExeEscape(p string) string {
	return strings.ReplaceAll(p, `"`, "")
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
