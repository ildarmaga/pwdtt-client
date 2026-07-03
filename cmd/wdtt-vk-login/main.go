//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const vkLoginURL = "https://vk.com/"

type statusFile struct {
	Done    bool   `json:"done"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Cookie  string `json:"cookie,omitempty"`
}

var (
	statusOut string
	done      atomic.Bool
)

func writeStatus(st statusFile) {
	if statusOut == "" {
		return
	}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(statusOut, b, 0600)
}

func findEdge() string {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func harvestCookies(ctx context.Context) (string, bool) {
	var cookies []*network.Cookie
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().WithUrls([]string{
			"https://vk.com/",
			"https://login.vk.com/",
			"https://id.vk.com/",
		}).Do(ctx)
		return err
	})); err != nil {
		return "", false
	}
	var remixsid, pCookie string
	for _, c := range cookies {
		dom := strings.ToLower(c.Domain)
		if !strings.HasPrefix(dom, ".") {
			dom = "." + dom
		}
		if c.Name == "remixsid" && strings.HasSuffix(dom, ".vk.com") && c.Value != "" {
			remixsid = c.Value
		}
		if c.Name == "p" && strings.HasSuffix(dom, ".login.vk.com") && c.Value != "" {
			pCookie = c.Value
		}
	}
	if remixsid == "" {
		return "", false
	}
	header := "remixsid=" + remixsid
	if pCookie != "" {
		header += "; p=" + pCookie
	}
	return header, true
}

func main() {
	out := flag.String("status", "", "status json path")
	dataDir := flag.String("data", "", "Edge user data dir")
	flag.Parse()
	statusOut = *out
	writeStatus(statusFile{Status: "waiting", Message: "Загрузка VK…"})

	edge := findEdge()
	if edge == "" {
		writeStatus(statusFile{Status: "error", Message: "Microsoft Edge не найден"})
		os.Exit(1)
	}

	profile := *dataDir
	if profile == "" {
		profile = filepath.Join(os.Getenv("APPDATA"), "pwdtt", "webview-vk", "profile")
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		writeStatus(statusFile{Status: "error", Message: err.Error()})
		os.Exit(1)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(edge),
		chromedp.Flag("user-data-dir", profile),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.WindowSize(520, 720),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	if err := chromedp.Run(ctx, chromedp.Navigate(vkLoginURL)); err != nil {
		writeStatus(statusFile{Status: "error", Message: "Не удалось открыть vk.com: " + err.Error()})
		os.Exit(1)
	}
	writeStatus(statusFile{Status: "waiting", Message: "Войдите в VK — cookies сохранятся автоматически"})

	ticker := time.NewTicker(1200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if !done.Load() {
				writeStatus(statusFile{Status: "cancelled", Message: "Вход отменён"})
			}
			return
		case <-ticker.C:
			header, ok := harvestCookies(ctx)
			if !ok {
				continue
			}
			done.Store(true)
			writeStatus(statusFile{Done: true, Status: "done", Message: "Cookies сохранены", Cookie: header})
			fmt.Println("ok")
			return
		}
	}
}
