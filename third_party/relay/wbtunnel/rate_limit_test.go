package wbtunnel

import (
	"bytes"
	"context"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestMbpsToLimiterNil(t *testing.T) {
	if mbpsToLimiter(0) != nil || mbpsToLimiter(-1) != nil {
		t.Fatal("expected nil limiter")
	}
}

func TestLimitedWriterThrottles(t *testing.T) {
	lim := mbpsToLimiter(0.001) // 0.001 MB/s ≈ 1 KB/s
	if lim == nil {
		t.Fatal("limiter")
	}
	var buf bytes.Buffer
	lw := &limitedWriter{
		ctx: context.Background(),
		w:   &buf,
		getLim: func() *rate.Limiter {
			return lim
		},
	}
	// Well above burst (4KB) at ~1KB/s so WaitN must sleep.
	payload := make([]byte, 12*1024)
	start := time.Now()
	if _, err := lw.Write(payload); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 4*time.Second {
		t.Fatalf("write too fast: %v (limit not applied?)", elapsed)
	}
	if buf.Len() != len(payload) {
		t.Fatalf("wrote %d", buf.Len())
	}
}
