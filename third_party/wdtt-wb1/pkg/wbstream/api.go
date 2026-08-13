package wbstream

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var (
	APIBase = "https://stream.wb.ru"
	Origin  = "https://stream.wb.ru"
)

// ParseRoomID принимает room id, wbstream:// или https://stream.wb.ru/room/<id>.
func ParseRoomID(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(trimmed, "wbstream://"); ok {
		return strings.Trim(rest, "/")
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		u, err := url.Parse(trimmed)
		if err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			for i := 0; i < len(parts)-1; i++ {
				if parts[i] == "room" && parts[i+1] != "" {
					return parts[i+1]
				}
			}
		}
	}
	return strings.Trim(trimmed, "/")
}

// JoinLink формирует ссылку для joiner.
func JoinLink(roomID string) string {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return ""
	}
	return "wbstream://" + roomID
}

type guestRegisterRequest struct {
	DisplayName string         `json:"displayName"`
	Device      guestDeviceCfg `json:"device"`
}

type guestDeviceCfg struct {
	DeviceName string `json:"deviceName"`
	DeviceType string `json:"deviceType"`
}

type guestRegisterResponse struct {
	AccessToken string `json:"accessToken"`
}

type createRoomRequest struct {
	RoomType    string `json:"roomType"`
	RoomPrivacy string `json:"roomPrivacy"`
}

type createRoomResponse struct {
	RoomID string `json:"roomId"`
}

type connectionDetailsResponse struct {
	RoomToken string `json:"roomToken"`
	ServerURL string `json:"serverUrl"`
}

type slideV3Response struct {
	Payload struct {
		AccessToken string `json:"access_token"`
	} `json:"payload"`
}

type cookieTransport struct {
	base   http.RoundTripper
	cookie string
}

func (t *cookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Cookie", t.cookie)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func clientWithCookies(client *http.Client, cookieHeader string) *http.Client {
	if cookieHeader == "" {
		return client
	}
	if client == nil {
		client = &http.Client{}
	}
	wrapped := *client
	wrapped.Transport = &cookieTransport{base: client.Transport, cookie: cookieHeader}
	return &wrapped
}

func httpDo(client *http.Client, req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", UserAgent)
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

func setBearer(req *http.Request, accessToken string) {
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RefreshAccessToken — slide-v3 refresh по cookies WB Stream.
func RefreshAccessToken(client *http.Client, cookieHeader, deviceID string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, "https://auth-stream.wb.ru/v2/auth/slide-v3", bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	if deviceID == "" {
		deviceID = newRequestID()
	}
	req.Header.Set("wb-apptype", "web")
	req.Header.Set("X-Real-IP", "")
	req.Header.Set("deviceId", deviceID)
	req.Header.Set("X-Request-ID", newRequestID())
	req.Header.Set("Origin", Origin)
	req.Header.Set("Referer", Origin+"/")
	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("User-Agent", UserAgent)

	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("slide-v3: status %d: %s", resp.StatusCode, string(raw))
	}
	var r slideV3Response
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("slide-v3 decode: %w", err)
	}
	if r.Payload.AccessToken == "" {
		return "", fmt.Errorf("slide-v3: empty access_token")
	}
	return r.Payload.AccessToken, nil
}

func createRoom(client *http.Client, accessToken string) (string, error) {
	body, _ := json.Marshal(createRoomRequest{
		RoomType:    "ROOM_TYPE_ALL_ON_SCREEN",
		RoomPrivacy: "ROOM_PRIVACY_FREE",
	})
	req, err := http.NewRequest(http.MethodPost, APIBase+"/api-room/api/v2/room", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	setBearer(req, accessToken)

	resp, err := httpDo(client, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("create-room: status %d: %s", resp.StatusCode, string(raw))
	}
	var r createRoomResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("create-room decode: %w", err)
	}
	if r.RoomID == "" {
		return "", fmt.Errorf("create-room: empty roomId")
	}
	return r.RoomID, nil
}

func joinRoom(client *http.Client, accessToken, roomID string) error {
	joinURL := fmt.Sprintf("%s/api-room/api/v1/room/%s/join", APIBase, roomID)
	req, err := http.NewRequest(http.MethodPost, joinURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setBearer(req, accessToken)

	resp, err := httpDo(client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("join-room: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func getConnectionDetails(client *http.Client, accessToken, roomID, displayName string) (string, string, error) {
	detailsURL := fmt.Sprintf("%s/api-room-manager/v2/room/%s/connection-details?deviceType=PARTICIPANT_DEVICE_TYPE_WEB_DESKTOP&displayName=%s",
		APIBase, roomID, url.QueryEscape(displayName))
	req, err := http.NewRequest(http.MethodGet, detailsURL, nil)
	if err != nil {
		return "", "", err
	}
	setBearer(req, accessToken)

	resp, err := httpDo(client, req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("connection-details: status %d: %s", resp.StatusCode, string(raw))
	}
	var r connectionDetailsResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", "", fmt.Errorf("connection-details decode: %w", err)
	}
	return r.RoomToken, r.ServerURL, nil
}

func joinAndGetDetails(client *http.Client, accessToken, roomID, displayName string) (string, string, string, string, error) {
	var err error
	if roomID == "" {
		roomID, err = createRoom(client, accessToken)
		if err != nil {
			return "", "", "", "", fmt.Errorf("create room: %w", err)
		}
	}
	if err := joinRoom(client, accessToken, roomID); err != nil {
		return "", "", "", "", fmt.Errorf("join room: %w", err)
	}
	roomToken, serverURL, err := getConnectionDetails(client, accessToken, roomID, displayName)
	if err != nil {
		return "", "", "", "", fmt.Errorf("connection details: %w", err)
	}
	return roomID, roomToken, accessToken, serverURL, nil
}

const guestRegisterPath = "/auth/api/v1/auth/user/guest-register"

// RegisterGuest создаёт гостевой access token WB Stream (платформенный HTTP).
func RegisterGuest(client *http.Client, displayName string) (string, error) {
	if strings.TrimSpace(displayName) == "" {
		displayName = "WDTT"
	}
	body, err := json.Marshal(guestRegisterRequest{
		DisplayName: displayName,
		Device: guestDeviceCfg{
			DeviceName: displayName,
			DeviceType: "PARTICIPANT_DEVICE_TYPE_WEB_DESKTOP",
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, APIBase+guestRegisterPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", Origin)
	req.Header.Set("Referer", Origin+"/")

	resp, err := httpDo(client, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("guest-register: status %d: %s", resp.StatusCode, string(raw))
	}
	var r guestRegisterResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("guest-register decode: %w", err)
	}
	if r.AccessToken == "" {
		return "", fmt.Errorf("guest-register: empty accessToken")
	}
	return r.AccessToken, nil
}

// AuthAsGuest присоединяется к существующей комнате как гость.
func AuthAsGuest(client *http.Client, displayName, roomID string) (string, string, string, string, error) {
	roomID = ParseRoomID(roomID)
	if roomID == "" {
		return "", "", "", "", fmt.Errorf("room id required")
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = "WDTT"
	}
	access, err := RegisterGuest(client, displayName)
	if err != nil {
		return "", "", "", "", err
	}
	return joinAndGetDetails(client, access, roomID, displayName)
}

// AuthAsLoggedIn создаёт или присоединяется к комнате от имени залогиненного пользователя.
func AuthAsLoggedIn(client *http.Client, cookieHeader, accessToken, roomID, displayName string) (string, string, string, string, error) {
	if cookieHeader == "" && accessToken == "" {
		return "", "", "", "", fmt.Errorf("cookies or access token required")
	}
	client = clientWithCookies(client, cookieHeader)
	return joinAndGetDetails(client, accessToken, ParseRoomID(roomID), displayName)
}

// CreateRoomResult — результат создания комнаты (фаза 1: только HTTP, без WebRTC).
type CreateRoomResult struct {
	RoomID    string
	JoinLink  string
	RoomToken string
	ServerURL string
}

// CreateRoomLoggedIn создаёт новую комнату WB Stream.
func CreateRoomLoggedIn(client *http.Client, cookieHeader, deviceID, displayName string) (CreateRoomResult, error) {
	filtered := FilterCookies(cookieHeader, CookieAllowlist)
	bearer, err := RefreshAccessToken(client, filtered, deviceID)
	if err != nil {
		return CreateRoomResult{}, fmt.Errorf("refresh token: %w", err)
	}
	if displayName == "" {
		displayName = "WDTT"
	}
	roomID, roomToken, _, serverURL, err := AuthAsLoggedIn(client, filtered, bearer, "", displayName)
	if err != nil {
		return CreateRoomResult{}, err
	}
	return CreateRoomResult{
		RoomID:    roomID,
		JoinLink:  JoinLink(roomID),
		RoomToken: roomToken,
		ServerURL: serverURL,
	}, nil
}
