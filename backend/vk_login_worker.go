//go:build windows

package backend

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const vkLoginWorkerFlag = "--vk-login-worker"

// MaybeRunVKLoginWorker handles hidden subprocess mode for de-elevated VK login.
func MaybeRunVKLoginWorker(args []string) (bool, error) {
	if len(args) == 0 || args[0] != vkLoginWorkerFlag {
		return false, nil
	}
	fs := flag.NewFlagSet("vk-login-worker", flag.ContinueOnError)
	status := fs.String("status", "", "status json path")
	data := fs.String("data", "", "Edge user data dir")
	if err := fs.Parse(args[1:]); err != nil {
		return true, err
	}
	return true, runVKLoginWorker(*status, *data)
}

func runVKLoginWorker(statusPath, profile string) error {
	workerPid := os.Getpid()
	writeSt := func(st vkLoginStatusFile) {
		st.Pid = workerPid
		writeVKLoginStatus(statusPath, st)
	}

	writeSt(vkLoginStatusFile{Status: "waiting", Message: "Загрузка VK…"})

	if isProcessElevated() {
		writeSt(vkLoginStatusFile{Status: "waiting", Message: "Предупреждение: worker elevated, пробуем Edge…"})
	}

	edge := findEdgeBrowser()
	if edge == "" {
		writeSt(vkLoginStatusFile{Status: "error", Message: "Microsoft Edge не найден"})
		return fmt.Errorf("edge not found")
	}

	if profile == "" {
		profile = vkLoginProfileDir()
	}
	_ = os.MkdirAll(filepath.Dir(vkLoginLogPath()), 0700)

	browserCtx, cleanup, err := startVKChromedp(context.Background(), edge, profile)
	if err != nil {
		writeSt(vkLoginStatusFile{Status: "error", Message: formatVKChromedpStartErr(err)})
		return err
	}
	defer cleanup()

	writeSt(vkLoginStatusFile{Status: "waiting", Message: "Войдите в VK — cookies сохранятся автоматически"})

	var done atomic.Bool
	ticker := time.NewTicker(1200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-browserCtx.Done():
			if !done.Load() {
				writeSt(vkLoginStatusFile{Status: "cancelled", Message: "Вход отменён"})
			}
			return browserCtx.Err()
		case <-ticker.C:
			header, ok := vkHarvestChromedp(browserCtx)
			if !ok {
				continue
			}
			done.Store(true)
			writeSt(vkLoginStatusFile{
				Done: true, Status: "done", Message: "Cookies сохранены", Cookie: header,
			})
			return nil
		}
	}
}

func writeVKLoginStatus(path string, st vkLoginStatusFile) {
	if path == "" {
		return
	}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(path, b, 0600)
}
