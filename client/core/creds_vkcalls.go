package core

import (
	"bytes"
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
	vkConnectClientID = "8093730"
	vkCallsAPIHost    = "api.vk.me"
	vkCallsAPIVersion = "5.276"
)

// getVKCredsViaVKCallsPath uses the VK Calls iOS captcha-free flow via api.vk.me
// instead of the legacy login.vk.ru path blocked on many whitelisted networks.
func getVKCredsViaVKCallsPath(ctx context.Context, linkID string, streamID int) (string, string, []string, error) {
	deviceID := uuid.New().String()
	name := generateName()
	ua := getRandomProfile().UserAgent
	linkURL := neturl.QueryEscape("https://vk.com/call/join/" + linkID)
	nameEnc := neturl.QueryEscape(name)

	log.Printf("[STREAM %d] [VK Calls] identity name=%s device_id=%s host=%s", streamID, name, deviceID, vkCallsAPIHost)

	client, err := newVKHTTPClient()
	if err != nil {
		return "", "", nil, fmt.Errorf("tls client: %w", err)
	}

	doRequest := func(url string) (map[string]interface{}, error) {
		req, err := fhttp.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(nil))
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
		req.Header.Set("Accept-Language", "en-GB,en;q=0.9")

		httpResp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer httpResp.Body.Close()

		body, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, err
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal: %w body=%s", err, truncateBody(string(body), 200))
		}
		return resp, nil
	}

	step1URL := fmt.Sprintf(
		"https://%s/method/auth.getAnonymToken?v=%s&client_id=%s&link=%s&device_id=%s&anonymName=%s&lang=en",
		vkCallsAPIHost, vkCallsAPIVersion, vkConnectClientID, linkURL, deviceID, nameEnc,
	)
	resp1, err := doRequest(step1URL)
	if err != nil {
		return "", "", nil, fmt.Errorf("step1 auth.getAnonymToken: %w", err)
	}
	anonymToken, err := extractStrFromResp(resp1, "response", "token")
	if err != nil {
		return "", "", nil, fmt.Errorf("step1 parse: %w (resp=%v)", err, truncResp(resp1))
	}
	anonymTokenEnc := neturl.QueryEscape(anonymToken)
	log.Printf("[STREAM %d] [VK Calls] step1 OK", streamID)

	step2URL := fmt.Sprintf(
		"https://%s/method/messages.getCallPreview?v=%s&anonymous_token=%s&device_id=%s&extended=1&fields=first_name,last_name,photo_200&lang=en&link=%s",
		vkCallsAPIHost, vkCallsAPIVersion, anonymTokenEnc, deviceID, linkURL,
	)
	resp2, err := doRequest(step2URL)
	if err != nil {
		return "", "", nil, fmt.Errorf("step2 messages.getCallPreview: %w", err)
	}
	if captchaErr := vkCallsCaptchaError(resp2); captchaErr != nil {
		return "", "", nil, captchaErr
	}
	userIDFloat, err := extractFloatFromResp(resp2, "response", "user_id")
	if err != nil {
		return "", "", nil, fmt.Errorf("step2 parse user_id: %w (resp=%v)", err, truncResp(resp2))
	}
	userIDStr := fmt.Sprintf("%.0f", userIDFloat)
	secret, err := extractStrFromResp(resp2, "response", "secret")
	if err != nil {
		return "", "", nil, fmt.Errorf("step2 parse secret: %w", err)
	}
	log.Printf("[STREAM %d] [VK Calls] step2 OK user_id=%s", streamID, userIDStr)

	step3URL := fmt.Sprintf(
		"https://%s/method/messages.getAnonymCallToken?v=%s&anonymous_token=%s&device_id=%s&link=%s&name=%s&user_id=%s&secret=%s&lang=en",
		vkCallsAPIHost, vkCallsAPIVersion, anonymTokenEnc, deviceID, linkURL, nameEnc, userIDStr, neturl.QueryEscape(secret),
	)
	resp3, err := doRequest(step3URL)
	if err != nil {
		return "", "", nil, fmt.Errorf("step3 messages.getAnonymCallToken: %w", err)
	}
	if captchaErr := vkCallsCaptchaError(resp3); captchaErr != nil {
		return "", "", nil, captchaErr
	}
	okAnonymToken, err := extractStrFromResp(resp3, "response", "token")
	if err != nil {
		return "", "", nil, fmt.Errorf("step3 parse: %w (resp=%v)", err, truncResp(resp3))
	}
	log.Printf("[STREAM %d] [VK Calls] step3 OK", streamID)

	okDeviceID := uuid.New().String()
	step4URL := "https://calls.okcdn.ru/fb.do?session_data=" +
		neturl.QueryEscape(fmt.Sprintf(`{"version":2,"device_id":"%s","client_version":"1.0.1"}`, okDeviceID)) +
		"&method=auth.anonymLogin&format=JSON&application_key=CGMMEJLGDIHBABABA"
	resp4, err := doRequest(step4URL)
	if err != nil {
		return "", "", nil, fmt.Errorf("step4 auth.anonymLogin: %w", err)
	}
	sessionKey, err := extractStrFromResp(resp4, "session_key")
	if err != nil {
		return "", "", nil, fmt.Errorf("step4 parse: %w (resp=%v)", err, truncResp(resp4))
	}
	log.Printf("[STREAM %d] [VK Calls] step4 OK", streamID)

	step5URL := fmt.Sprintf(
		"https://calls.okcdn.ru/fb.do?joinLink=%s&isVideo=false&protocolVersion=5&anonymToken=%s&method=vchat.joinConversationByLink&format=JSON&application_key=CGMMEJLGDIHBABABA&session_key=%s",
		linkID, okAnonymToken, sessionKey,
	)
	resp5, err := doRequest(step5URL)
	if err != nil {
		return "", "", nil, fmt.Errorf("step5 vchat.joinConversationByLink: %w", err)
	}

	user, err := extractStrFromResp(resp5, "turn_server", "username")
	if err != nil {
		return "", "", nil, fmt.Errorf("step5 parse username: %w (resp=%v)", err, truncResp(resp5))
	}
	pass, err := extractStrFromResp(resp5, "turn_server", "credential")
	if err != nil {
		return "", "", nil, fmt.Errorf("step5 parse credential: %w", err)
	}
	addresses := parseTURNAddressesFromResp(resp5)
	if len(addresses) == 0 {
		return "", "", nil, fmt.Errorf("step5: turn_server.urls empty")
	}

	log.Printf("[STREAM %d] [VK Calls] SUCCESS username=%s urls=%d", streamID, user, len(addresses))
	return user, pass, addresses, nil
}

func vkCallsCaptchaError(resp map[string]interface{}) error {
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		return nil
	}
	if _, hasCaptcha := errObj["captcha_sid"]; hasCaptcha {
		return fmt.Errorf("CAPTCHA_WAIT_REQUIRED: vkcalls path captcha gate")
	}
	if code, ok := errObj["error_code"].(float64); ok && code != 0 {
		return fmt.Errorf("VK API error: %v", errObj)
	}
	return nil
}

func extractStrFromResp(resp map[string]interface{}, keys ...string) (string, error) {
	var cur interface{} = resp
	for _, k := range keys {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("expected map at %q, got %T", k, cur)
		}
		cur = m[k]
	}
	s, ok := cur.(string)
	if !ok {
		return "", fmt.Errorf("expected string, got %T", cur)
	}
	return s, nil
}

func extractFloatFromResp(resp map[string]interface{}, keys ...string) (float64, error) {
	var cur interface{} = resp
	for _, k := range keys {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return 0, fmt.Errorf("expected map at %q, got %T", k, cur)
		}
		cur = m[k]
	}
	f, ok := cur.(float64)
	if !ok {
		return 0, fmt.Errorf("expected float64, got %T", cur)
	}
	return f, nil
}

func parseTURNAddressesFromResp(resp map[string]interface{}) []string {
	turnServer, ok := resp["turn_server"].(map[string]interface{})
	if !ok {
		return nil
	}
	urls, ok := turnServer["urls"].([]interface{})
	if !ok {
		return nil
	}
	var addrs []string
	for _, u := range urls {
		s, ok := u.(string)
		if !ok {
			continue
		}
		clean := strings.Split(s, "?")[0]
		addr := strings.TrimPrefix(strings.TrimPrefix(clean, "turn:"), "turns:")
		addrs = append(addrs, addr)
	}
	return addrs
}

func truncateBody(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func truncResp(resp map[string]interface{}) string {
	b, err := json.Marshal(resp)
	if err != nil {
		return fmt.Sprintf("%v", resp)
	}
	return truncateBody(string(b), 300)
}
