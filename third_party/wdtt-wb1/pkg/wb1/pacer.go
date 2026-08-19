package wb1

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	pacerWrappedRateBits = 16_000_000 // conservative WB LiveKit baseline; adaptive feedback may reduce it
	pacerMinRateBits     = 4_000_000
	pacerMaxRateBits     = 24_000_000
	pacerBurstBytes      = 24 * 1024 // 24 KiB quantum; avoids Windows ~1ms/pkt cap
)

var errPacerBurst = errors.New("wb1: sample exceeds pacer burst")

// bytePacer is a synchronous token bucket. Wait serializes token accounting
// only; sleep happens without holding pacer.mu so concurrent callers share
// the configured aggregate rate.
type bytePacer struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	tokens  float64
	last    time.Time
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	healthy int
}

func (p *bytePacer) ObserveWrite(d time.Duration, failed bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case failed || d >= 750*time.Millisecond:
		p.rate *= 0.50
		p.healthy = 0
	case d >= 200*time.Millisecond:
		p.rate *= 0.75
		p.healthy = 0
	case d <= 80*time.Millisecond:
		p.healthy++
		if p.healthy >= 64 {
			p.rate *= 1.10
			p.healthy = 0
		}
	default:
		p.healthy = 0
	}
	minRate, maxRate := float64(pacerMinRateBits)/8, float64(pacerMaxRateBits)/8
	if p.rate < minRate {
		p.rate = minRate
	}
	if p.rate > maxRate {
		p.rate = maxRate
	}
}

func (p *bytePacer) RateBits() int64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return int64(p.rate * 8)
}

func newBytePacer(rateBytesPerSec float64, burst int) *bytePacer {
	if rateBytesPerSec <= 0 {
		rateBytesPerSec = float64(pacerWrappedRateBits) / 8
	}
	if burst < 1 {
		burst = pacerBurstBytes
	}
	p := &bytePacer{
		rate:   rateBytesPerSec,
		burst:  float64(burst),
		tokens: float64(burst),
		now:    time.Now,
		sleep:  sleepContext,
	}
	p.last = p.now()
	return p
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (p *bytePacer) Wait(ctx context.Context, n int) error {
	if p == nil {
		return ctx.Err()
	}
	if n < 0 {
		n = 0
	}
	if n > int(p.burst) {
		return fmt.Errorf("%w: %d > %d", errPacerBurst, n, int(p.burst))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.refillLocked()
		need := float64(n)
		if p.tokens >= need {
			p.tokens -= need
			return nil
		}
		deficit := need - p.tokens
		refill := p.burst
		if deficit > refill {
			refill = deficit
		}
		wait := time.Duration(refill / p.rate * float64(time.Second))
		p.mu.Unlock()
		err := p.sleep(ctx, wait)
		p.mu.Lock()
		if err != nil {
			return err
		}
	}
}

func (p *bytePacer) refillLocked() {
	now := p.now()
	if p.last.IsZero() {
		p.last = now
		return
	}
	dt := now.Sub(p.last).Seconds()
	p.last = now
	if dt <= 0 {
		return
	}
	p.tokens += dt * p.rate
	if p.tokens > p.burst {
		p.tokens = p.burst
	}
}
