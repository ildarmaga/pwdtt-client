//go:build windows

package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"wg-turn-client/core"
)

var (
	vkLoginWinMu      sync.Mutex
	vkLoginCmd        *exec.Cmd
	vkLoginStatusPath string
)

type vkLoginStatusFile struct {
	Done    bool   `json:"done"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Cookie  string `json:"cookie,omitempty"`
}

func (a *App) StartVKLogin() (VKLoginStartResult, error) {
	vkLoginWinMu.Lock()
	defer vkLoginWinMu.Unlock()

	if vkLoginCmd != nil && vkLoginCmd.Process != nil {
		return VKLoginStartResult{Active: true, Native: true}, nil
	}

	helper, err := vkLoginHelperExe()
	if err != nil {
		return VKLoginStartResult{}, err
	}

	statusDir := filepath.Join(os.Getenv("APPDATA"), "pwdtt", "vk-login")
	if err := os.MkdirAll(statusDir, 0700); err != nil {
		return VKLoginStartResult{}, err
	}
	vkLoginStatusPath = filepath.Join(statusDir, "status.json")
	_ = os.Remove(vkLoginStatusPath)

	dataDir := filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "profile")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return VKLoginStartResult{}, err
	}

	cmd := exec.Command(helper, "--status", vkLoginStatusPath, "--data", dataDir)
	if err := cmd.Start(); err != nil {
		return VKLoginStartResult{}, fmt.Errorf("не удалось открыть окно VK: %w", err)
	}
	vkLoginCmd = cmd
	return VKLoginStartResult{Active: true, Native: true}, nil
}

func (a *App) StopVKLogin() {
	vkLoginWinMu.Lock()
	defer vkLoginWinMu.Unlock()
	stopVKLoginHelperLocked()
}

func stopVKLoginHelperLocked() {
	if vkLoginCmd != nil && vkLoginCmd.Process != nil {
		_ = vkLoginCmd.Process.Kill()
		_, _ = vkLoginCmd.Process.Wait()
	}
	vkLoginCmd = nil
}

func (a *App) PollVKLogin() VKLoginPollResult {
	vkLoginWinMu.Lock()
	path := vkLoginStatusPath
	cmd := vkLoginCmd
	vkLoginWinMu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return VKLoginPollResult{Status: "idle"}
	}
	if path == "" {
		return VKLoginPollResult{Status: "waiting", Message: "Войдите в VK — ожидаем remixsid…"}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return VKLoginPollResult{Status: "waiting", Message: "Войдите в VK — ожидаем remixsid…"}
	}
	var st vkLoginStatusFile
	if err := json.Unmarshal(data, &st); err != nil {
		return VKLoginPollResult{Status: "waiting", Message: "Войдите в VK — ожидаем remixsid…"}
	}
	if st.Status == "error" {
		return VKLoginPollResult{Status: "error", Message: st.Message}
	}
	if !st.Done || st.Cookie == "" {
		msg := st.Message
		if msg == "" {
			msg = "Войдите в VK — ожидаем remixsid…"
		}
		return VKLoginPollResult{Status: "waiting", Message: msg}
	}

	if err := core.SaveVKCookiesJSON([]byte(st.Cookie)); err != nil {
		return VKLoginPollResult{Status: "error", Message: err.Error()}
	}
	_ = core.SetVKUseCookies(true)

	vkLoginWinMu.Lock()
	stopVKLoginHelperLocked()
	vkLoginWinMu.Unlock()
	return VKLoginPollResult{Done: true, Status: "done", Message: "Cookies сохранены"}
}

func vkLoginHelperExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	helper := filepath.Join(filepath.Dir(exe), "wdtt-vk-login.exe")
	if _, err := os.Stat(helper); err == nil {
		return helper, nil
	}
	return "", fmt.Errorf("wdtt-vk-login.exe не найден рядом с программой — переустановите WDTT")
}
