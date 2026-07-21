//go:build windows

package backend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"wg-turn-client/core"
)

var vkLoginWin struct {
	sync.Mutex
	cancel    context.CancelFunc
	status    string
	errMsg    string
	done      bool
	cookie    string
	active    bool
	helperPid uint32
}

type vkLoginStatusFile struct {
	Done    bool   `json:"done"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Cookie  string `json:"cookie,omitempty"`
	Pid     int    `json:"pid,omitempty"`
}

func vkLoginDataDir() string {
	return filepath.Join(DataDir(), "webview-vk")
}

func processAlive(pid uint32) bool {
	if pid == 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}

func (a *App) StartVKLogin() (VKLoginStartResult, error) {
	vkLoginWin.Lock()
	defer vkLoginWin.Unlock()

	if vkLoginWin.active {
		return VKLoginStartResult{Active: true, Native: true}, nil
	}

	dataDir := vkLoginDataDir()
	statusPath := filepath.Join(dataDir, "status.json")

	// If a previous harvest left the worker window open, attach to it instead
	// of taskkill + wipe profile (that was closing the QR window ~1s later
	// whenever StartVKLogin ran again after helper exited on "done").
	if st, err := readVKLoginStatus(statusPath); err == nil && st.Pid > 0 && processAlive(uint32(st.Pid)) {
		ctx, cancel := context.WithCancel(context.Background())
		vkLoginWin.cancel = cancel
		vkLoginWin.active = true
		vkLoginWin.done = false
		vkLoginWin.errMsg = ""
		vkLoginWin.cookie = ""
		vkLoginWin.helperPid = uint32(st.Pid)
		vkLoginWin.status = st.Message
		if vkLoginWin.status == "" {
			vkLoginWin.status = "Окно VK уже открыто…"
		}
		go a.runVKLoginHelper(ctx, true)
		return VKLoginStartResult{Active: true, Native: true}, nil
	}

	// Independent of app ctx — quitting/re-render of Wails must not cancel the
	// login worker (that killed the window via StopVKLogin → killProcessTree).
	ctx, cancel := context.WithCancel(context.Background())
	vkLoginWin.cancel = cancel
	vkLoginWin.active = true
	vkLoginWin.done = false
	vkLoginWin.errMsg = ""
	vkLoginWin.cookie = ""
	vkLoginWin.helperPid = 0
	vkLoginWin.status = "Загрузка VK…"

	go a.runVKLoginHelper(ctx, false)
	return VKLoginStartResult{Active: true, Native: true}, nil
}

func (a *App) StopVKLogin() {
	vkLoginWin.Lock()
	cancel := vkLoginWin.cancel
	pid := vkLoginWin.helperPid
	vkLoginWin.Unlock()
	if cancel != nil {
		cancel()
	}
	logBase := filepath.Join(vkLoginDataDir(), "profile")
	if pid != 0 {
		vkLoginLog(logBase, "StopVKLogin kill pid=%d", pid)
		killProcessTree(pid)
		return
	}
	statusPath := filepath.Join(vkLoginDataDir(), "status.json")
	if st, err := readVKLoginStatus(statusPath); err == nil && st.Pid > 0 {
		vkLoginLog(logBase, "StopVKLogin kill status.pid=%d", st.Pid)
		killProcessTree(uint32(st.Pid))
	}
}

// runVKLoginHelper spawns (or attaches to) the --vk-login-worker subprocess and
// polls status.json. On "done" it saves cookies but keeps polling until the user
// closes the window (cancelled) or StopVKLogin — so active stays true and a
// remount cannot taskkill the live worker.
func (a *App) runVKLoginHelper(ctx context.Context, attachOnly bool) {
	defer func() {
		vkLoginWin.Lock()
		vkLoginWin.active = false
		vkLoginWin.cancel = nil
		vkLoginWin.helperPid = 0
		vkLoginWin.Unlock()
	}()

	dataDir := vkLoginDataDir()
	profile := filepath.Join(dataDir, "profile")
	statusPath := filepath.Join(dataDir, "status.json")
	_ = os.MkdirAll(dataDir, 0700)

	if !attachOnly {
		exe, err := os.Executable()
		if err != nil {
			vkLoginWin.Lock()
			vkLoginWin.errMsg = err.Error()
			vkLoginWin.Unlock()
			return
		}

		// Only kill a recorded PID if that process is already dead / stale.
		// Never taskkill a still-living worker — that closes the QR window.
		if st, err := readVKLoginStatus(statusPath); err == nil && st.Pid > 0 {
			if processAlive(uint32(st.Pid)) {
				vkLoginLog(dataDir, "spawn: live worker pid=%d — attaching instead of kill", st.Pid)
				vkLoginWin.Lock()
				vkLoginWin.helperPid = uint32(st.Pid)
				vkLoginWin.Unlock()
			} else {
				vkLoginLog(dataDir, "spawn: stale pid=%d gone, wiping profile", st.Pid)
				_ = os.Remove(statusPath)
				_ = os.RemoveAll(profile)
			}
		} else {
			_ = os.Remove(statusPath)
			_ = os.RemoveAll(profile)
		}

		vkLoginWin.Lock()
		already := vkLoginWin.helperPid
		vkLoginWin.Unlock()
		if already == 0 {
			cmd := execDetachedUI(exe, vkLoginWorkerFlag, "-status", statusPath, "-data", profile)
			cmd.Dir = filepath.Dir(exe)
			if err := cmd.Start(); err != nil {
				vkLoginWin.Lock()
				vkLoginWin.errMsg = "Не удалось запустить окно VK: " + err.Error()
				vkLoginWin.Unlock()
				return
			}
			vkLoginWin.Lock()
			vkLoginWin.helperPid = uint32(cmd.Process.Pid)
			vkLoginWin.status = "Открываем окно VK…"
			vkLoginWin.Unlock()
			vkLoginLog(dataDir, "spawned worker pid=%d", cmd.Process.Pid)
			go func() { _, _ = cmd.Process.Wait() }()
		}
	}

	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	savedDone := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st, err := readVKLoginStatus(statusPath)
			if err != nil {
				continue
			}
			if st.Pid > 0 {
				vkLoginWin.Lock()
				vkLoginWin.helperPid = uint32(st.Pid)
				vkLoginWin.Unlock()
				if !processAlive(uint32(st.Pid)) {
					// After successful harvest WM_DESTROY does not rewrite status
					// (done stays). Either way the window is gone — stop helper.
					if !savedDone && st.Status != "cancelled" && st.Status != "error" {
						vkLoginWin.Lock()
						vkLoginWin.errMsg = "Окно VK неожиданно закрылось (процесс завершился)"
						vkLoginWin.Unlock()
						vkLoginLog(dataDir, "worker pid=%d died unexpectedly status=%q", st.Pid, st.Status)
					} else {
						vkLoginLog(dataDir, "worker pid=%d exited status=%q", st.Pid, st.Status)
					}
					return
				}
			}
			switch st.Status {
			case "error":
				vkLoginWin.Lock()
				vkLoginWin.errMsg = st.Message
				vkLoginWin.Unlock()
				return
			case "done":
				if st.Done && st.Cookie != "" && !savedDone {
					vkLoginWin.Lock()
					if err := core.SaveVKCookiesJSON([]byte(st.Cookie)); err != nil {
						vkLoginWin.errMsg = err.Error()
						vkLoginWin.Unlock()
						return
					}
					_ = core.SetVKUseCookies(true)
					vkLoginWin.done = true
					vkLoginWin.cookie = st.Cookie
					vkLoginWin.status = st.Message
					vkLoginWin.Unlock()
					savedDone = true
					vkLoginLog(dataDir, "cookies saved — keeping helper until window closes")
				}
				// Stay active until cancelled / process exit — do NOT return.
			case "cancelled":
				return
			default:
				vkLoginWin.Lock()
				if st.Message != "" {
					vkLoginWin.status = st.Message
				}
				vkLoginWin.Unlock()
			}
		}
	}
}

func readVKLoginStatus(path string) (vkLoginStatusFile, error) {
	var st vkLoginStatusFile
	b, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	return st, nil
}

func (a *App) PollVKLogin() VKLoginPollResult {
	vkLoginWin.Lock()
	defer vkLoginWin.Unlock()

	if vkLoginWin.errMsg != "" {
		return VKLoginPollResult{Status: "error", Message: vkLoginWin.errMsg}
	}
	if !vkLoginWin.active && !vkLoginWin.done {
		return VKLoginPollResult{Status: "idle"}
	}
	if vkLoginWin.done && vkLoginWin.cookie != "" {
		if err := core.SaveVKCookiesJSON([]byte(vkLoginWin.cookie)); err != nil {
			return VKLoginPollResult{Status: "error", Message: err.Error()}
		}
		_ = core.SetVKUseCookies(true)
		vkLoginWin.done = false
		vkLoginWin.cookie = ""
		return VKLoginPollResult{Done: true, Status: "done", Message: "Cookies сохранены"}
	}
	msg := vkLoginWin.status
	if msg == "" {
		msg = "Войдите в VK — ожидаем remixsid…"
	}
	return VKLoginPollResult{Status: "waiting", Message: msg}
}
