package wbstream

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthAsGuest(t *testing.T) {
	var sawRegister, sawJoin, sawDetails bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/auth/api/v1/auth/user/guest-register"):
			sawRegister = true
			var body guestRegisterRequest
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("register json: %v", err)
			}
			if body.DisplayName != "WDTT-DEV" {
				t.Errorf("displayName %q", body.DisplayName)
			}
			_ = json.NewEncoder(w).Encode(guestRegisterResponse{AccessToken: "guest-token"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api-room/api/v1/room/") && strings.HasSuffix(r.URL.Path, "/join"):
			sawJoin = true
			if got := r.Header.Get("Authorization"); got != "Bearer guest-token" {
				t.Errorf("join auth %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api-room-manager/v2/room/"):
			sawDetails = true
			_ = json.NewEncoder(w).Encode(connectionDetailsResponse{
				RoomToken: "lk-jwt",
				ServerURL: "wss://livekit.example",
			})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	old := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = old })

	room, token, access, server, err := AuthAsGuest(srv.Client(), "WDTT-DEV", "room-abc")
	if err != nil {
		t.Fatal(err)
	}
	if !sawRegister || !sawJoin || !sawDetails {
		t.Fatalf("calls register=%v join=%v details=%v", sawRegister, sawJoin, sawDetails)
	}
	if room != "room-abc" || token != "lk-jwt" || access != "guest-token" || server != "wss://livekit.example" {
		t.Fatalf("got room=%s token=%s access=%s server=%s", room, token, access, server)
	}
}
