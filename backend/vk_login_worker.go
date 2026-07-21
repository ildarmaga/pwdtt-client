//go:build windows

package backend

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const vkLoginWorkerFlag = "--vk-login-worker"

// MaybeRunVKLoginWorker handles hidden subprocess mode for the VK login window.
func MaybeRunVKLoginWorker(args []string) (bool, error) {
	if len(args) == 0 || args[0] != vkLoginWorkerFlag {
		return false, nil
	}
	fs := flag.NewFlagSet("vk-login-worker", flag.ContinueOnError)
	status := fs.String("status", "", "status json path")
	data := fs.String("data", "", "WebView2 user data dir")
	if err := fs.Parse(args[1:]); err != nil {
		return true, err
	}
	return true, runVKLoginWorker(*status, *data)
}

func runVKLoginWorker(statusPath, profile string) (err error) {
	workerPid := os.Getpid()
	writeSt := func(st vkLoginStatusFile) {
		st.Pid = workerPid
		writeVKLoginStatus(statusPath, st)
	}

	defer func() {
		if r := recover(); r != nil {
			writeSt(vkLoginStatusFile{Status: "error", Message: fmt.Sprintf("сбой worker VK: %v", r)})
			err = fmt.Errorf("vk login worker panic: %v", r)
		}
	}()

	writeSt(vkLoginStatusFile{Status: "waiting", Message: "Загрузка VK…"})

	if profile == "" {
		profile = filepath.Join(DataDir(), "webview-vk", "profile")
	}
	return runVKWebView2Window(profile, writeSt)
}

func writeVKLoginStatus(path string, st vkLoginStatusFile) {
	if path == "" {
		return
	}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(path, b, 0600)
}
