package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	vkCookieCheckAppID = "6287487"
	vkCookieCheckTTL   = 3 * time.Minute
	vkCookieExpiredHint = "Cookies устарели — обновите remixsid (войдите на vk.com и сохраните заново)."
)

var (
	vkCookieCheckMu sync.Mutex
	vkCookieCheckAt time.Time
	vkCookieCheckOK bool
	vkCookieCheckClient = &http.Client{Timeout: 30 * time.Second}
	vkCookieValidateLive = func(cookieHeader string) error {
		return vkCheckWebToken(cookieHeader)
	}
)

func invalidateVKCookieStatusCache() {
	vkCookieCheckMu.Lock()
	vkCookieCheckAt = time.Time{}
	vkCookieCheckMu.Unlock()
}

func vkCheckWebToken(cookieHeader string) error {
	form := url.Values{"version": {"1"}, "app_id": {vkCookieCheckAppID}}
	req, err := http.NewRequest(http.MethodPost, "https://login.vk.com/?act=web_token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Origin", "https://vk.com")
	req.Header.Set("Referer", "https://vk.com/")
	req.Header.Set("Cookie", cookieHeader)
	resp, err := vkCookieCheckClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var tok struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return fmt.Errorf("web_token parse: %w", err)
	}
	if tok.Data.AccessToken == "" {
		return fmt.Errorf("empty access_token")
	}
	return nil
}

func vkCookiesLiveValid(header string) error {
	vkCookieCheckMu.Lock()
	defer vkCookieCheckMu.Unlock()
	if !vkCookieCheckAt.IsZero() && time.Since(vkCookieCheckAt) < vkCookieCheckTTL {
		if vkCookieCheckOK {
			return nil
		}
		return fmt.Errorf(vkCookieExpiredHint)
	}
	err := vkCookieValidateLive(header)
	vkCookieCheckAt = time.Now()
	vkCookieCheckOK = err == nil
	return err
}
