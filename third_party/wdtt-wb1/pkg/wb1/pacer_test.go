package wb1

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.Advance(d)
	return nil
}

func testPacer(rateBps float64, burst int, clk *fakeClock) *bytePacer {
	p := newBytePacer(rateBps, burst)
	p.now = clk.Now
	p.sleep = clk.Sleep
	p.last = clk.Now()
	p.tokens = float64(burst)
	return p
}

func TestPacerStartsWithBoundedBurstNotWindow(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := testPacer(1e6, pacerBurstBytes, clk)
	if p.tokens != float64(pacerBurstBytes) {
		t.Fatalf("initial tokens %v, want burst %d", p.tokens, pacerBurstBytes)
	}
	if pacerBurstBytes < 16*1024 || pacerBurstBytes > 32*1024 {
		t.Fatalf("burst %d outside 16–32KiB", pacerBurstBytes)
	}
	if pacerWrappedRateBits < pacerMinRateBits || pacerWrappedRateBits > pacerMaxRateBits {
		t.Fatalf("wrapped rate %d outside adaptive bounds", pacerWrappedRateBits)
	}
	if pacerBurstBytes >= ARQWindow*MaxPayload {
		t.Fatalf("burst must not start as cwnd1024 dump (%d bytes)", pacerBurstBytes)
	}
}

func TestPacerAdaptsToLiveKitBackpressure(t *testing.T) {
	p := newBytePacer(float64(pacerWrappedRateBits)/8, pacerBurstBytes)
	start := p.RateBits()
	p.ObserveWrite(time.Second, false)
	if got := p.RateBits(); got >= start {
		t.Fatalf("slow WriteSample did not reduce rate: %d >= %d", got, start)
	}
	for i := 0; i < 20; i++ {
		p.ObserveWrite(2*time.Second, false)
	}
	if got := p.RateBits(); got != pacerMinRateBits {
		t.Fatalf("rate floor %d, want %d", got, pacerMinRateBits)
	}
	for i := 0; i < 64; i++ {
		p.ObserveWrite(10*time.Millisecond, false)
	}
	if got := p.RateBits(); got <= pacerMinRateBits {
		t.Fatalf("healthy writes did not recover rate: %d", got)
	}
}

func TestPacerTokenAccountingNoSleepWhileBurst(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	var sleeps atomic.Int32
	p := testPacer(1_000, 100, clk)
	p.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps.Add(1)
		return clk.Sleep(ctx, d)
	}
	ctx := context.Background()
	if err := p.Wait(ctx, 40); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(ctx, 40); err != nil {
		t.Fatal(err)
	}
	if sleeps.Load() != 0 {
		t.Fatalf("slept %d times inside burst", sleeps.Load())
	}
	if err := p.Wait(ctx, 30); err != nil {
		t.Fatal(err)
	}
	if sleeps.Load() < 1 {
		t.Fatal("must sleep after burst is exhausted")
	}
}

func TestPacerWriteLatencyConsumesBudget(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	var sleeps atomic.Int32
	p := testPacer(1_000, 50, clk)
	p.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps.Add(1)
		return clk.Sleep(ctx, d)
	}
	ctx := context.Background()
	if err := p.Wait(ctx, 50); err != nil {
		t.Fatal(err)
	}
	// WriteSample took 80ms → 80 bytes of budget; next 50-byte sample must not sleep.
	clk.Advance(80 * time.Millisecond)
	if err := p.Wait(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if sleeps.Load() != 0 {
		t.Fatalf("slept %d times though WriteSample already consumed budget", sleeps.Load())
	}
}

func TestPacerContextCancel(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := testPacer(1, 1, clk)
	p.tokens = 0
	p.sleep = func(ctx context.Context, _ time.Duration) error {
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Wait(ctx, 1); err == nil {
		t.Fatal("want context error")
	}
}

func TestPacerRejectsLargerThanBurst(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := testPacer(1_000, 100, clk)
	err := p.Wait(context.Background(), 101)
	if err == nil || !errors.Is(err, errPacerBurst) {
		t.Fatalf("want errPacerBurst, got %v", err)
	}
	if p.tokens != 100 {
		t.Fatalf("tokens changed on reject: %v", p.tokens)
	}
}

func TestPacerPayloadCapUsesConservativeWBBaseline(t *testing.T) {
	key := testKey(t)
	f := Frame{
		Type:     TypeData,
		Dest:     testSID(1),
		Src:      testSID(2),
		Epoch:    1,
		Seq:      1,
		StreamID: 1,
		Payload:  make([]byte, MaxPayload),
	}
	wire, err := Pack(key, f)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := len(WrapVP8(testSID(1), wire))
	if wrapped <= 0 {
		t.Fatal("wrapped")
	}
	ratio := float64(MaxPayload) / float64(wrapped)
	if ratio < 0.87 || ratio > 0.91 {
		t.Logf("payload/wrapped = %.4f (len %d); expected ~87–91%%", ratio, wrapped)
	}
	capBits := float64(pacerWrappedRateBits) * ratio
	if capBits < 14e6 || capBits > 22e6 {
		t.Fatalf("payload cap %.2f Mbps at wrapped %d bps (sample %d B), want safe WB baseline", capBits/1e6, pacerWrappedRateBits, wrapped)
	}
}

func TestPacerWallClockSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("wall clock")
	}
	p := newBytePacer(float64(pacerWrappedRateBits)/8, pacerBurstBytes)
	const total = 256 * 1024
	start := time.Now()
	left := total
	ctx := context.Background()
	for left > 0 {
		n := 1200
		if n > left {
			n = left
		}
		if err := p.Wait(ctx, n); err != nil {
			t.Fatal(err)
		}
		left -= n
	}
	elapsed := time.Since(start)
	mbps := float64(total) * 8 / elapsed.Seconds() / 1e6
	t.Logf("wall-clock pacer %.2f Mbps over %s (configured %.1f Mbps wrapped)", mbps, elapsed, float64(pacerWrappedRateBits)/1e6)
	if elapsed < 5*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("elapsed %s out of loose bounds", elapsed)
	}
}

func TestVP8WriteStatsAtomics(t *testing.T) {
	s := newTestSession(testSID(1))
	s.noteWriteSample(nil, 3*time.Millisecond)
	s.noteWriteSample(context.Canceled, 9*time.Millisecond)
	st := s.VP8WriteStats()
	if st.Count != 2 || st.Errors != 1 {
		t.Fatalf("stats %+v", st)
	}
	if st.Last != 9*time.Millisecond {
		t.Fatalf("last %s", st.Last)
	}
	if st.Max != 9*time.Millisecond {
		t.Fatalf("max %s", st.Max)
	}
	s.ResetVP8WriteStats()
	st = s.VP8WriteStats()
	if st.Count != 0 || st.Errors != 0 || st.Max != 0 {
		t.Fatalf("after reset %+v", st)
	}
}

func TestTwoJoinersSharePacerWithoutHoldingVideoMu(t *testing.T) {
	s := newTestSession(testSID(1))
	sleeping := make(chan struct{})
	release := make(chan struct{})
	var sleepers atomic.Int32
	s.pacer.tokens = 0
	s.pacer.last = time.Now()
	s.pacer.sleep = func(ctx context.Context, _ time.Duration) error {
		if sleepers.Add(1) == 1 {
			close(sleeping)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.writeHook = func([]byte, time.Duration) error { return nil }

	errA := make(chan error, 1)
	go func() {
		errA <- s.writeVP8(context.Background(), testSID(2), []byte("a"))
	}()
	select {
	case <-sleeping:
	case <-time.After(2 * time.Second):
		t.Fatal("A never entered pacer sleep")
	}

	locked := make(chan struct{})
	go func() {
		s.videoMu.Lock()
		close(locked)
		s.videoMu.Unlock()
	}()
	select {
	case <-locked:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("videoMu held while A sleeps in pacer")
	}

	errB := make(chan error, 1)
	go func() {
		errB <- s.writeVP8(context.Background(), testSID(3), []byte("b"))
	}()
	deadline := time.Now().Add(2 * time.Second)
	for sleepers.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("B could not enter pacer independently")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-errA; err != nil {
		t.Fatal(err)
	}
	if err := <-errB; err != nil {
		t.Fatal(err)
	}
	st := s.VP8WriteStats()
	if st.Count != 2 || st.Errors != 0 {
		t.Fatalf("writes %+v", st)
	}
}

func TestPacerConcurrentAggregateStaysBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("wall clock")
	}
	p := newBytePacer(float64(pacerWrappedRateBits)/8, pacerBurstBytes)
	const per = 128 * 1024
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			left := per
			for left > 0 {
				n := 1200
				if n > left {
					n = left
				}
				if err := p.Wait(context.Background(), n); err != nil {
					t.Error(err)
					return
				}
				left -= n
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	mbps := float64(2*per) * 8 / elapsed.Seconds() / 1e6
	t.Logf("two joiners aggregate %.2f Mbps over %s (configured %.1f)", mbps, elapsed, float64(pacerWrappedRateBits)/1e6)
	if mbps > 80 || mbps < 15 {
		t.Fatalf("aggregate %.2f Mbps; want ~50 shared, not ~100", mbps)
	}
}
