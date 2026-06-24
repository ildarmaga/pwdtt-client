//go:build live

package core

import (
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

// go test -tags=live ./core/ -run TestLiveVKCreds -v -timeout 3m
func TestLiveVKCreds(t *testing.T) {
	linkID := os.Getenv("VK_JOIN_ID")
	if linkID == "" {
		linkID = "VvvYkdeBROis4m_E9NSjARVybExrC1xGPxA4m9M18t0"
	}
	linkID = normalizeVKJoinHash(linkID)
	if linkID == "" {
		t.Fatal("empty join link id")
	}

	resolveVKHostsOnce()
	captchaCh := make(chan string, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	t.Logf("testing VK creds for join id=%s...", linkID)

	user, pass, addrs, err := GetCreds(ctx, linkID, 100, captchaCh,
		func() string { return "auto" },
		func(mode, redirectURI, sessionToken string) {},
	)
	if err != nil {
		t.Fatalf("GetCreds failed: %v", err)
	}
	if user == "" || pass == "" || len(addrs) == 0 {
		t.Fatalf("empty creds user=%q pass_len=%d addrs=%d", user, len(pass), len(addrs))
	}
	log.Printf("SUCCESS user=%s pass_len=%d turn_urls=%d first=%s", user, len(pass), len(addrs), addrs[0])
	if strings.Contains(err.Error(), "anonym_token") {
		t.Fatal("still getting anonym_token error")
	}
}
