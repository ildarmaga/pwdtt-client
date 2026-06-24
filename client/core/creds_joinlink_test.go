package core

import "testing"

func TestVKOKJoinLink(t *testing.T) {
	resp := map[string]interface{}{
		"response": map[string]interface{}{
			"ok_join_link": "abc123token",
		},
	}
	if got := vkOKJoinLink("fallback", resp); got != "abc123token" {
		t.Fatalf("got %q want abc123token", got)
	}
	resp["response"] = map[string]interface{}{
		"join_link": "https://vk.com/call/join/xyz789",
	}
	if got := vkOKJoinLink("fallback", resp); got != "xyz789" {
		t.Fatalf("got %q want xyz789", got)
	}
	if got := vkOKJoinLink("fallback", nil); got != "fallback" {
		t.Fatalf("got %q want fallback", got)
	}
}

func TestVKOKCDNError(t *testing.T) {
	if got := vkOKCDNError(map[string]interface{}{"error_code": float64(4), "error_msg": "token not found"}); got == "" {
		t.Fatal("expected error string")
	}
	if got := vkOKCDNError(map[string]interface{}{"turn_server": map[string]interface{}{}}); got != "" {
		t.Fatalf("got %q want empty", got)
	}
}
