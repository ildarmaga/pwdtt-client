//go:build live

package core

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveVKCookieCreds(t *testing.T) {
	linkID := os.Getenv("VK_JOIN_ID")
	if linkID == "" {
		linkID = "VvvYkdeBROis4m_E9NSjARVybExrC1xGPxA4m9M18t0"
	}
	linkID = normalizeVKJoinHash(linkID)

	header, err := LoadVKCookieHeader()
	if err != nil {
		t.Skipf("no cookies: %v", err)
	}

	resolveVKHostsOnce()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	user, pass, addrs, err := getVKCredsViaCookies(ctx, linkID, 100, header)
	if err != nil {
		t.Fatalf("cookie creds: %v", err)
	}
	if user == "" || pass == "" || len(addrs) == 0 {
		t.Fatalf("empty result user=%q addrs=%d", user, len(addrs))
	}
	t.Logf("OK user=%s urls=%d first=%s", user, len(addrs), addrs[0])
}
