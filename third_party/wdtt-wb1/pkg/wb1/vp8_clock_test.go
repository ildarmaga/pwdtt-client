package wb1

import (
	"sync"
	"testing"
	"time"
)

func pionRTPTicks(d time.Duration) int {
	// Pion mediaengine: samples = duration.Seconds() * clockRate
	return int(d.Seconds() * float64(vp8ClockRate))
}

func pionRTPTicksInteger(d time.Duration) int {
	return int(float64(d) / float64(time.Second) * float64(vp8ClockRate))
}

func TestVP8MinTickYieldsAtLeastOnePionTick(t *testing.T) {
	if pionRTPTicks(vp8MinTick) < 1 {
		t.Fatalf("vp8MinTick %s → Pion duration.Seconds()*90000 = %d, want >= 1", vp8MinTick, pionRTPTicks(vp8MinTick))
	}
	if pionRTPTicksInteger(vp8MinTick) < 1 {
		t.Fatalf("vp8MinTick integer equivalent = %d, want >= 1", pionRTPTicksInteger(vp8MinTick))
	}
	var c vp8SampleClock
	d0 := c.Next()
	if pionRTPTicks(d0) < 1 {
		t.Fatalf("first Next() %s yields 0 RTP ticks", d0)
	}
	if vp8MaxSample != 100*time.Millisecond {
		t.Fatalf("max sample %s, want 100ms", vp8MaxSample)
	}
}

func TestVP8SampleDurationWallClockDelta(t *testing.T) {
	var c vp8SampleClock
	d0 := c.Next()
	if d0 < vp8MinTick {
		t.Fatalf("first duration %s below one 90kHz tick", d0)
	}
	time.Sleep(5 * time.Millisecond)
	d1 := c.Next()
	if d1 < vp8MinTick {
		t.Fatalf("duration %s below one tick", d1)
	}
	if d1 > vp8MaxSample {
		t.Fatalf("duration %s exceeds cap", d1)
	}
	if d1 < 2*time.Millisecond || d1 > 50*time.Millisecond {
		t.Fatalf("wall-clock delta %s, want ~5ms", d1)
	}
}

func TestVP8SampleDurationPositiveAndCapped(t *testing.T) {
	var c vp8SampleClock
	c.last = time.Now().Add(-time.Second)
	d := c.Next()
	if d <= 0 {
		t.Fatal("must be positive")
	}
	if d > vp8MaxSample {
		t.Fatalf("got %s want <= %s", d, vp8MaxSample)
	}
}

func TestVP8SampleDurationConcurrent(t *testing.T) {
	var c vp8SampleClock
	var wg sync.WaitGroup
	errCh := make(chan time.Duration, 32*100)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				d := c.Next()
				if d <= 0 || d > vp8MaxSample {
					errCh <- d
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for d := range errCh {
		t.Fatalf("invalid concurrent duration %s", d)
	}
}
