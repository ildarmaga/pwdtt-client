package backend

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ReportWBPresence сообщает панели device_id активного WB-клиента (подсветка в «Пользователи»).
// subURL — URL подписки (.../sub/<id>); POST идёт на .../wb/auth.
func (a *App) ReportWBPresence(subURL, password, deviceID string) {
	subURL = strings.TrimSpace(subURL)
	password = strings.TrimSpace(password)
	deviceID = strings.TrimSpace(deviceID)
	if subURL == "" || password == "" || deviceID == "" {
		return
	}
	go func() {
		_ = postWBPresence(subURL, password, deviceID)
		// refresh while session alive
		for i := 0; i < 40; i++ {
			time.Sleep(45 * time.Second)
			if !a.wb.IsRunning() {
				return
			}
			_ = postWBPresence(subURL, password, deviceID)
		}
	}()
}

func postWBPresence(subURL, password, deviceID string) error {
	authURL := strings.TrimRight(subURL, "/")
	if idx := strings.Index(strings.ToLower(authURL), "/wb/auth"); idx >= 0 {
		authURL = authURL[:idx] + "/wb/auth"
	} else {
		authURL = authURL + "/wb/auth"
	}
	body, _ := json.Marshal(map[string]string{
		"password":  password,
		"device_id": deviceID,
	})
	req, err := http.NewRequest(http.MethodPost, authURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "WDTT-Desktop/"+AppVersion)
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
