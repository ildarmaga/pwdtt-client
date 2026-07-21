package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const updateRepo = "ildarmaga/pwdtt-client"

type UpdateInfo struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	HasUpdate   bool   `json:"hasUpdate"`
	DownloadURL string `json:"downloadURL"`
	ReleaseURL  string `json:"releaseURL"`
	CheckedAt   string `json:"checkedAt"`
	Error       string `json:"error,omitempty"`
}

type UpdateApplyResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type UpdateProgress struct {
	Phase   string `json:"phase"`
	Percent int    `json:"percent"`
	Written int64  `json:"written"`
	Total   int64  `json:"total"`
	Message string `json:"message"`
}

type ghReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string           `json:"tag_name"`
	HTMLURL string           `json:"html_url"`
	Assets  []ghReleaseAsset `json:"assets"`
}

func (a *App) CheckForUpdate() UpdateInfo {
	return a.checkForUpdate(context.Background())
}

func (a *App) checkForUpdate(ctx context.Context) UpdateInfo {
	cur := a.GetAppVersion()
	out := UpdateInfo{
		Current:   cur,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if cur == "dev" {
		out.Error = "dev build"
		return out
	}

	// While WDTT VPN is up, hit GitHub through the tunnel (browser path).
	// Direct ISP egress is often blocked in RU and only worked for regions
	// where GitHub is reachable without a proxy.
	viaTunnel := a.IsTunnelRunning()
	if !viaTunnel {
		defer withUpdateDirectEgress()()
	}
	client := newUpdateHTTPClient(12*time.Second, viaTunnel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+updateRepo+"/releases/latest", nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "WDTT-Desktop/"+cur)

	resp, err := client.Do(req)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if resp.StatusCode != http.StatusOK {
		out.Error = fmt.Sprintf("GitHub API %d", resp.StatusCode)
		return out
	}

	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		out.Error = err.Error()
		return out
	}
	latest := strings.TrimSpace(rel.TagName)
	if latest == "" {
		out.Error = "empty release tag"
		return out
	}
	out.Latest = latest
	out.ReleaseURL = rel.HTMLURL
	out.DownloadURL = pickAssetName(rel.Assets, "wdtt-windows-amd64.exe")
	if out.DownloadURL == "" {
		out.DownloadURL = pickWindowsAsset(rel.Assets)
	}
	if out.DownloadURL == "" && latest != "" {
		out.DownloadURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/wdtt-windows-amd64.exe", updateRepo, latest)
	}
	out.HasUpdate = versionLess(cur, latest)
	return out
}

func pickAssetName(assets []ghReleaseAsset, name string) string {
	want := strings.ToLower(name)
	for _, a := range assets {
		if strings.EqualFold(a.Name, name) || strings.ToLower(a.Name) == want {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

func pickWindowsAsset(assets []ghReleaseAsset) string {
	if u := pickAssetName(assets, "wdtt-windows-amd64.exe"); u != "" {
		return u
	}
	for _, a := range assets {
		n := strings.ToLower(a.Name)
		if strings.Contains(n, "windows") && strings.HasSuffix(n, ".exe") && !strings.Contains(n, "vk-login") {
			return a.BrowserDownloadURL
		}
	}
	for _, a := range assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, ".exe") && !strings.Contains(n, "vk-login") {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

func versionLess(current, latest string) bool {
	return compareVersionTags(current, latest) < 0
}

func compareVersionTags(a, b string) int {
	pa := parseVersionTag(a)
	pb := parseVersionTag(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseVersionTag(v string) [3]int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	parts := strings.SplitN(v, "-", 2)[0]
	seg := strings.Split(parts, ".")
	var out [3]int
	for i := 0; i < len(seg) && i < 3; i++ {
		var n int
		fmt.Sscanf(seg[i], "%d", &n)
		out[i] = n
	}
	return out
}
