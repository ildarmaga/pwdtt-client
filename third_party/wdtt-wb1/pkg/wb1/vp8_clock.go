package wb1

import (
	"sync"
	"time"
)

const (
	vp8ClockRate = 90000
	// Ceiling nanoseconds so Pion's duration.Seconds()*90000 is at least 1.
	vp8MinTick   = time.Duration((int64(time.Second) + vp8ClockRate - 1) / vp8ClockRate)
	vp8MaxSample = 100 * time.Millisecond
)

// vp8SampleClock turns wall-clock gaps into RTP sample durations so high-pps
// WriteSample does not advance the 90 kHz clock at a fixed 1 ms per packet.
type vp8SampleClock struct {
	mu   sync.Mutex
	last time.Time
}

func (c *vp8SampleClock) Next() time.Duration {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last.IsZero() {
		c.last = now
		return vp8MinTick
	}
	d := now.Sub(c.last)
	c.last = now
	if d < vp8MinTick {
		d = vp8MinTick
	}
	if d > vp8MaxSample {
		d = vp8MaxSample
	}
	return d
}
