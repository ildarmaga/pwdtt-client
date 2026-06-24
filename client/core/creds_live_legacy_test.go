//go:build live

package core

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	tlsclient "github.com/bogdanfinn/tls-client"
)

// go test -tags=live ./core/ -run TestLiveVKLegacyCreds -v -timeout 3m
func TestLiveVKLegacyCreds(t *testing.T) {
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
	jar := tlsclient.NewCookieJar()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	t.Logf("testing legacy VK creds for join id=%s...", linkID)

	user, pass, addrs, err := getTokenChain(ctx, linkID, 100, vkCredentialsList[0], jar, captchaCh,
		func() string { return "auto" },
		func(mode, redirectURI, sessionToken string) {},
	)
	if err != nil {
		t.Fatalf("getTokenChain failed: %v", err)
	}
	if user == "" || pass == "" || len(addrs) == 0 {
		t.Fatalf("empty creds user=%q pass_len=%d addrs=%d", user, len(pass), len(addrs))
	}
	log.Printf("LEGACY SUCCESS user=%s pass_len=%d turn_urls=%d first=%s", user, len(pass), len(addrs), addrs[0])
}
