//go:build windows

package backend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

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
	return filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk")
}

func (a *App) StartVKLogin() (VKLoginStartResult, error) {
	vkLoginWin.Lock()
	defer vkLoginWin.Unlock()

	if vkLoginWin.active {
		return VKLoginStartResult{Active: true, Native: true}, nil
	}

	ctx, cancel := context.WithCancel(a.ctx)
	vkLoginWin.cancel = cancel
	vkLoginWin.active = true
	vkLoginWin.done = false
	vkLoginWin.errMsg = ""
	vkLoginWin.cookie = ""
	vkLoginWin.helperPid = 0
	vkLoginWin.status = "Загрузка VK…"

	go a.runVKLoginHelper(ctx)
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
	if pid != 0 {
		killProcessTree(pid)
		return
	}
	statusPath := filepath.Join(vkLoginDataDir(), "status.json")
	if st, err := readVKLoginStatus(statusPath); err == nil && st.Pid > 0 {
		killProcessTree(uint32(st.Pid))
	}
}

// runVKLoginHelper spawns this same exe as a --vk-login-worker subprocess that
// shows a native WebView2 window (like the iOS WKWebView flow) and polls its
// status file for harvested cookies.
func (a *App) runVKLoginHelper(ctx context.Context) {
	defer func() {
		vkLoginWin.Lock()
		vkLoginWin.active = false
		vkLoginWin.cancel = nil
		vkLoginWin.helperPid = 0
		vkLoginWin.Unlock()
	}()

	exe, err := os.Executable()
	if err != nil {
		vkLoginWin.Lock()
		vkLoginWin.errMsg = err.Error()
		vkLoginWin.Unlock()
		return
	}

	dataDir := vkLoginDataDir()
	profile := filepath.Join(dataDir, "profile")
	statusPath := filepath.Join(dataDir, "status.json")
	_ = os.MkdirAll(dataDir, 0700)

	// Kill orphaned worker and wipe WebView2 profile so stale remixsid from a
	// previous session cannot auto-close the window in ~1s (blank page, no login).
	if st, err := readVKLoginStatus(statusPath); err == nil && st.Pid > 0 {
		killProcessTree(uint32(st.Pid))
	}
	_ = os.Remove(statusPath)
	_ = os.RemoveAll(profile)

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

	go func() { _, _ = cmd.Process.Wait() }()

	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st, err := readVKLoginStatus(statusPath)
			if err != nil {
				continue
			}
			switch st.Status {
			case "error":
				vkLoginWin.Lock()
				vkLoginWin.errMsg = st.Message
				vkLoginWin.Unlock()
				return
			case "done":
				if st.Done && st.Cookie != "" {
					vkLoginWin.Lock()
					if err := core.SaveVKCookiesJSON([]byte(st.Cookie)); err != nil {
						vkLoginWin.errMsg = err.Error()
					} else {
						_ = core.SetVKUseCookies(true)
						vkLoginWin.done = true
						vkLoginWin.cookie = st.Cookie
						vkLoginWin.status = st.Message
					}
					vkLoginWin.Unlock()
				}
				return
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
