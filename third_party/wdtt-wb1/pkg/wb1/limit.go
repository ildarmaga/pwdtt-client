package wb1

import (
	"context"
	"io"
	"net"
	"sync"

	"golang.org/x/time/rate"
)

// ByteLimiter paces copies in bytes/sec. 0 = unlimited.
type ByteLimiter struct {
	mu   sync.Mutex
	lim  *rate.Limiter
	mbps float64
}

func (b *ByteLimiter) SetMBps(mbps float64) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mbps = mbps
	if mbps <= 0 {
		b.lim = nil
		return
	}
	bps := rate.Limit(mbps * 1024 * 1024)
	burst := int(bps)
	if burst < 1024 {
		burst = 1024
	}
	b.lim = rate.NewLimiter(bps, burst)
}

func (b *ByteLimiter) WaitN(n int) {
	if b == nil || n <= 0 {
		return
	}
	b.mu.Lock()
	lim := b.lim
	b.mu.Unlock()
	if lim == nil {
		return
	}
	_ = lim.WaitN(context.Background(), n)
}

type limitedWriter struct {
	w net.Conn
	l *ByteLimiter
}

func (w limitedWriter) Write(p []byte) (int, error) {
	if w.l != nil {
		w.l.WaitN(len(p))
	}
	return w.w.Write(p)
}

// CopyBothLimited is CopyBoth with optional ↓/↑ byte limiters.
func CopyBothLimited(a, b net.Conn, down, up *ByteLimiter) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(limitedWriter{w: a, l: down}, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(limitedWriter{w: b, l: up}, a)
		done <- struct{}{}
	}()
	<-done
}
