package backend

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type wbAuthResponse struct {
	OK     bool   `json:"ok"`
	WbRoom string `json:"wb_room"`
	Error  string `json:"error"`
}

// wbAuthURLFromSubURL строит POST …/sub/wb/auth из URL подписки …/sub/<id>.
func wbAuthURLFromSubURL(subURL string) string {
	subURL = strings.TrimSpace(subURL)
	subURL = strings.Split(subURL, "?")[0]
	subURL = strings.TrimRight(subURL, "/")
	lower := strings.ToLower(subURL)
	if idx := strings.Index(lower, "/wb/auth"); idx >= 0 {
		return subURL[:idx] + "/wb/auth"
	}
	if i := strings.LastIndex(subURL, "/"); i > 0 {
		return subURL[:i] + "/wb/auth"
	}
	return subURL + "/wb/auth"
}

func postWBAuth(subURL, password, deviceID string) (*wbAuthResponse, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return nil, nil
	}
	authURL := wbAuthURLFromSubURL(subURL)
	payload := map[string]string{"password": password}
	if id := strings.TrimSpace(deviceID); id != "" {
		payload["device_id"] = id
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, authURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "WDTT-Desktop/"+AppVersion)
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, err
	}
	var out wbAuthResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FetchWBRoomFromPanel возвращает wb_room через POST /sub/wb/auth (если creator активен).
func (a *App) FetchWBRoomFromPanel(subURL, password string) (string, error) {
	resp, err := postWBAuth(subURL, password, "")
	if err != nil {
		return "", err
	}
	if resp == nil || !resp.OK {
		if resp != nil && resp.Error != "" {
			return "", nil
		}
		return "", nil
	}
	return strings.TrimSpace(resp.WbRoom), nil
}

// ReportWBPresence сообщает панели device_id активного WB-клиента (подсветка в «Пользователи»).
func (a *App) ReportWBPresence(subURL, password, deviceID string) {
	subURL = strings.TrimSpace(subURL)
	password = strings.TrimSpace(password)
	deviceID = strings.TrimSpace(deviceID)
	if subURL == "" || password == "" || deviceID == "" {
		return
	}
	go func() {
		_, _ = postWBAuth(subURL, password, deviceID)
		for i := 0; i < 40; i++ {
			time.Sleep(45 * time.Second)
			if !a.wb.IsRunning() {
				return
			}
			_, _ = postWBAuth(subURL, password, deviceID)
		}
	}()
}
