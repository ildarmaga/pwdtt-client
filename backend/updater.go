//go:build windows

package backend

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

// IsTunnelRunning reports whether VK or WB tunnel is active.
func (a *App) IsTunnelRunning() bool {
	return a.orch.IsRunning() || a.wb.IsRunning()
}

// GetUpdateDownloadState returns the last emitted update progress (for UI re-sync).
func (a *App) GetUpdateDownloadState() UpdateProgress {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return a.updateProgress
}

// IsUpdateDownloading reports whether a download/apply is in progress.
func (a *App) IsUpdateDownloading() bool {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return a.updateActive
}

// DownloadAndApplyUpdate downloads the latest Windows exe and schedules replace+restart.
func (a *App) DownloadAndApplyUpdate() UpdateApplyResult {
	if a.IsUpdateDownloading() {
		return UpdateApplyResult{Message: "Обновление уже скачивается"}
	}
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
	a.setUpdateActive(true)
	defer a.setUpdateActive(false)
	if err := a.downloadFileWithProgress(url, newExe); err != nil {
		a.emitUpdateProgress(UpdateProgress{Phase: "error", Message: err.Error()})
		return UpdateApplyResult{Message: "Скачивание: " + err.Error()}
	}
	if err := verifyWindowsExe(newExe); err != nil {
		a.emitUpdateProgress(UpdateProgress{Phase: "error", Message: err.Error()})
		return UpdateApplyResult{Message: "Скачанный файл повреждён: " + err.Error()}
	}

	a.emitUpdateProgress(UpdateProgress{Phase: "applying", Percent: 100, Message: "Установка…"})

	// The downloaded exe applies the update itself: waits for us to exit,
	// copies itself over the old exe and relaunches it. No cmd/vbs/powershell —
	// nothing to flash a console, and elevation is inherited (no second UAC).
	cmd := execDetached(newExe, updateApplyFlag,
		"-pid", strconv.Itoa(os.Getpid()),
		"-dest", exePath,
	)
	cmd.Dir = updateDir
	if err := cmd.Start(); err != nil {
		return UpdateApplyResult{Message: "Не удалось запустить обновление: " + err.Error()}
	}

	a.quitting.Store(true)
	a.orch.Stop()
	a.wb.Disconnect()
	time.Sleep(150 * time.Millisecond)
	os.Exit(0)

	return UpdateApplyResult{OK: true, Message: fmt.Sprintf("Обновление до %s…", info.Latest)}
}

const updateApplyFlag = "--apply-update"

// MaybeRunUpdateApply handles the hidden self-update mode: this (new) exe was
// started from the update dir and must replace the old exe and relaunch it.
func MaybeRunUpdateApply(args []string) bool {
	if len(args) == 0 || args[0] != updateApplyFlag {
		return false
	}
	fs := flag.NewFlagSet("apply-update", flag.ContinueOnError)
	pid := fs.Int("pid", 0, "pid of the exiting app")
	dest := fs.String("dest", "", "path of the exe to replace")
	_ = fs.Parse(args[1:])
	runUpdateApply(*pid, *dest)
	return true
}

func runUpdateApply(pid int, dest string) {
	logPath := filepath.Join(os.Getenv("LOCALAPPDATA"), "WDTT", "update", "apply.log")
	logf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	logln := func(format string, args ...any) {
		if logf != nil {
			fmt.Fprintf(logf, "[%s] "+format+"\n", append([]any{time.Now().Format("15:04:05")}, args...)...)
		}
	}
	defer func() {
		if logf != nil {
			_ = logf.Close()
		}
	}()

	logln("apply start: pid=%d dest=%s", pid, dest)
	if dest == "" {
		logln("FAIL: empty dest")
		return
	}

	src, err := os.Executable()
	if err != nil {
		logln("FAIL: os.Executable: %v", err)
		return
	}

	// 1. Wait for the old app to exit (frees the exe file).
	waitProcessExit(pid, 60*time.Second)
	time.Sleep(1 * time.Second)
	logln("old process gone")

	// 2. Copy self over dest, retrying while AV/handles release the file.
	var lastErr error
	ok := false
	for i := 0; i < 120; i++ {
		if lastErr = replaceExe(src, dest); lastErr == nil {
			ok = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !ok {
		logln("FAIL: copy: %v", lastErr)
		// Old exe may be renamed to .old — restore it so the user keeps a working app.
		if _, err := os.Stat(dest); err != nil {
			if err := os.Rename(dest+".old", dest); err == nil {
				logln("restored old exe")
				_ = execDetachedUI(dest, ShowWindowFlag).Start()
			}
		}
		return
	}
	_ = os.Remove(dest + ".old")
	logln("copy ok")

	// 3. Relaunch with --show-window so the app opens visibly, not only in tray.
	if err := execDetachedUI(dest, ShowWindowFlag).Start(); err != nil {
		logln("FAIL: relaunch: %v", err)
		return
	}
	logln("relaunch ok")
}

// waitProcessExit polls until the pid disappears or the timeout passes.
func waitProcessExit(pid int, max time.Duration) {
	if pid <= 0 {
		return
	}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return // process gone
		}
		var code uint32
		errCode := windows.GetExitCodeProcess(h, &code)
		_ = windows.CloseHandle(h)
		if errCode != nil || code != 259 { // 259 = STILL_ACTIVE
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// replaceExe swaps dest with src: dest → dest.old, copy src → dest, verify size.
func replaceExe(src, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		_ = os.Remove(dest + ".old")
		if err := os.Rename(dest, dest+".old"); err != nil {
			return fmt.Errorf("rename old: %w", err)
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	sf, _ := os.Stat(src)
	df, _ := os.Stat(dest)
	if sf == nil || df == nil || sf.Size() != df.Size() {
		return fmt.Errorf("size mismatch after copy")
	}
	return nil
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

func (a *App) setUpdateActive(active bool) {
	a.updateMu.Lock()
	a.updateActive = active
	if !active && a.updateProgress.Phase == "downloading" {
		a.updateProgress = UpdateProgress{}
	}
	a.updateMu.Unlock()
}

func (a *App) emitUpdateProgress(p UpdateProgress) {
	a.updateMu.Lock()
	a.updateProgress = p
	if p.Phase == "downloading" || p.Phase == "applying" {
		a.updateActive = true
	} else if p.Phase == "error" {
		a.updateActive = false
	}
	a.updateMu.Unlock()
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
