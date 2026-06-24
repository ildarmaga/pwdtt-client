package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	neturl "net/url"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/google/uuid"
)

const (
	vkCookieAppID      = "6287487"
	vkCookieAPIVersion = "5.280"
	vkCookieAppVersion = "1.1"
)

// getVKCredsViaCookies uses logged-in VK session (remixsid) — works when anonymous join is blocked.
func getVKCredsViaCookies(ctx context.Context, linkID string, streamID int, cookieHeader string) (string, string, []string, error) {
	cookieHeader = strings.TrimSpace(cookieHeader)
	if cookieHeader == "" {
		return "", "", nil, fmt.Errorf("empty cookie header")
	}
	joinLink := strings.TrimSpace(linkID)
	if joinLink == "" {
		return "", "", nil, fmt.Errorf("empty join link")
	}

	client, err := newVKHTTPClient()
	if err != nil {
		return "", "", nil, fmt.Errorf("tls client: %w", err)
	}

	doForm := func(endpoint string, form neturl.Values, bearer string) (map[string]interface{}, error) {
		req, err := fhttp.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", getRandomProfile().UserAgent)
		req.Header.Set("Origin", "https://vk.com")
		req.Header.Set("Referer", "https://vk.com/")
		req.Header.Set("Cookie", cookieHeader)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		var out map[string]interface{}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("json: %w body=%s", err, truncateBody(string(body), 200))
		}
		return out, nil
	}

	log.Printf("[STREAM %d] [VK Cookie] web_token...", streamID)
	webResp, err := doForm("https://login.vk.com/?act=web_token",
		neturl.Values{"version": {"1"}, "app_id": {vkCookieAppID}}, "")
	if err != nil {
		return "", "", nil, fmt.Errorf("web_token: %w", err)
	}
	dataMap, _ := webResp["data"].(map[string]interface{})
	vkToken, _ := dataMap["access_token"].(string)
	if vkToken == "" {
		return "", "", nil, fmt.Errorf("web_token: empty access_token (cookies expired?) resp=%v", truncResp(webResp))
	}

	settingsResp, err := doForm("https://api.vk.com/method/calls.getSettings",
		neturl.Values{"v": {vkCookieAPIVersion}}, vkToken)
	if err != nil {
		return "", "", nil, fmt.Errorf("calls.getSettings: %w", err)
	}
	if errObj := vkAPIError(settingsResp); errObj != "" {
		return "", "", nil, fmt.Errorf("calls.getSettings: %s", errObj)
	}
	appKey, err := extractStrFromResp(settingsResp, "response", "settings", "public_key")
	if err != nil {
		// flat settings in some responses
		appKey, err = extractStrFromResp(settingsResp, "response", "public_key")
	}
	if err != nil || appKey == "" {
		appKey = "CGMMEJLGDIHBABABA"
	}

	callTokenResp, err := doForm("https://api.vk.com/method/messages.getCallToken",
		neturl.Values{"v": {vkCookieAPIVersion}, "env": {"production"}}, vkToken)
	if err != nil {
		return "", "", nil, fmt.Errorf("messages.getCallToken: %w", err)
	}
	if errObj := vkAPIError(callTokenResp); errObj != "" {
		return "", "", nil, fmt.Errorf("messages.getCallToken: %s", errObj)
	}
	authToken, err := extractStrFromResp(callTokenResp, "response", "token")
	if err != nil {
		return "", "", nil, fmt.Errorf("messages.getCallToken parse: %w (resp=%v)", err, truncResp(callTokenResp))
	}
	apiBaseURL, err := extractStrFromResp(callTokenResp, "response", "api_base_url")
	if err != nil {
		return "", "", nil, fmt.Errorf("messages.getCallToken api_base_url: %w", err)
	}
	apiBaseURL = strings.TrimRight(apiBaseURL, "/")
	if !strings.HasSuffix(apiBaseURL, "/fb.do") {
		apiBaseURL += "/fb.do"
	}

	deviceID := uuid.New().String()
	sessionData, _ := json.Marshal(map[string]interface{}{
		"version":        3,
		"device_id":      deviceID,
		"client_version": vkCookieAppVersion,
		"client_type":    "SDK_JS",
		"auth_token":     authToken,
	})
	loginResp, err := doForm(apiBaseURL, neturl.Values{
		"method":          {"auth.anonymLogin"},
		"application_key": {appKey},
		"format":          {"JSON"},
		"session_data":    {string(sessionData)},
	}, "")
	if err != nil {
		return "", "", nil, fmt.Errorf("auth.anonymLogin: %w", err)
	}
	if okErr := vkOKCDNError(loginResp); okErr != "" {
		return "", "", nil, fmt.Errorf("auth.anonymLogin: %s", okErr)
	}
	sessionKey, err := extractStrFromResp(loginResp, "session_key")
	if err != nil {
		return "", "", nil, fmt.Errorf("auth.anonymLogin parse: %w (resp=%v)", err, truncResp(loginResp))
	}

	okJoinLink := joinLink
	previewResp, err := doForm("https://api.vk.com/method/calls.getCallPreview",
		neturl.Values{"v": {vkCookieAPIVersion}, "vk_join_link": {"https://vk.com/call/join/" + joinLink}}, vkToken)
	if err == nil {
		if jl := vkOKJoinLink(joinLink, previewResp); jl != "" {
			okJoinLink = jl
		}
	}
	if okJoinLink != joinLink {
		log.Printf("[STREAM %d] [VK Cookie] ok_join_link=%s", streamID, okJoinLink)
	}

	joinResp, err := doForm(apiBaseURL, neturl.Values{
		"method":          {"vchat.joinConversationByLink"},
		"session_key":     {sessionKey},
		"application_key": {appKey},
		"format":          {"JSON"},
		"joinLink":        {okJoinLink},
		"isVideo":         {"false"},
		"isAudio":         {"false"},
		"protocolVersion": {"5"},
		"capabilities":    {"2F7F"},
	}, "")
	if err != nil {
		return "", "", nil, fmt.Errorf("joinConversationByLink: %w", err)
	}
	if okErr := vkOKCDNError(joinResp); okErr != "" {
		return "", "", nil, fmt.Errorf("joinConversationByLink: %s", okErr)
	}

	user, err := extractStrFromResp(joinResp, "turn_server", "username")
	if err != nil {
		return "", "", nil, fmt.Errorf("turn_server username: %w (resp=%v)", err, truncResp(joinResp))
	}
	pass, err := extractStrFromResp(joinResp, "turn_server", "credential")
	if err != nil {
		return "", "", nil, fmt.Errorf("turn_server credential: %w", err)
	}
	addrs := parseTURNAddressesFromResp(joinResp)
	if len(addrs) == 0 {
		return "", "", nil, fmt.Errorf("turn_server.urls empty")
	}

	log.Printf("[STREAM %d] [VK Cookie] SUCCESS user=%s urls=%d", streamID, user, len(addrs))
	return user, pass, addrs, nil
}

func vkAPIError(resp map[string]interface{}) string {
	if resp == nil {
		return ""
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		return ""
	}
	code, _ := errObj["error_code"].(float64)
	if code == 0 {
		return ""
	}
	msg, _ := errObj["error_msg"].(string)
	return fmt.Sprintf("error_code:%.0f error_msg:%s", code, msg)
}
