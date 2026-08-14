package wb1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"testing"
	"time"
)

func startMuxPairTimeout(t *testing.T, left, right Carrier, d time.Duration) (*Mux, *Mux, context.CancelFunc) {
	t.Helper()
	key := testKey(t)
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), d)
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	return joiner, creator, cancel
}

func runExact8MiB(t *testing.T, left, right Carrier, timeout time.Duration) float64 {
	t.Helper()
	joiner, creator, cancel := startMuxPairTimeout(t, left, right, timeout+5*time.Second)
	defer cancel()
	defer left.(*delayedEndpoint).Close()
	defer right.(*delayedEndpoint).Close()

	payload := makeThroughputPayload()
	sum := sha256.Sum256(payload)

	ctx, cancel2 := context.WithTimeout(context.Background(), timeout)
	defer cancel2()

	errCh := make(chan error, 1)
	go func() {
		_, conn, err := creator.Accept(ctx)
		if err != nil {
			errCh <- err
			return
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, got); err != nil {
			errCh <- err
			return
		}
		if sha256.Sum256(got) != sum {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		errCh <- nil
	}()

	start := time.Now()
	conn, err := joiner.Dial(ctx, "tp.example:443")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatalf("8MiB transfer timed out after %s", time.Since(start))
	}
	elapsed := time.Since(start)
	if elapsed <= 0 {
		elapsed = time.Nanosecond
	}
	mbps := float64(len(payload)) * 8 / elapsed.Seconds() / 1e6
	t.Logf("payload %d bytes in %s = %.2f Mbps", len(payload), elapsed, mbps)
	if !bytes.Equal(payload[:4], payload[:4]) {
		t.Fatal("sanity")
	}
	return mbps
}

func requireThroughput(t *testing.T, mbps float64) {
	t.Helper()
	if raceBuild {
		t.Logf("race build: %.2f Mbps (hash ok; 30.0 Mbps not asserted under -race)", mbps)
		return
	}
	if mbps < throughputMinMbps {
		t.Fatalf("payload throughput %.2f Mbps, want >= %.1f", mbps, throughputMinMbps)
	}
}

func TestWB1ThroughputClean72ms(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput")
	}
	left, right := newDelayedPair(0, false)
	requireThroughput(t, runExact8MiB(t, left, right, 45*time.Second))
}

func TestWB1ThroughputLoss1Pct(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput")
	}
	left, right := newDelayedPair(1, false)
	requireThroughput(t, runExact8MiB(t, left, right, 90*time.Second))
}

func TestWB1ThroughputLoss3PctReorder(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput")
	}
	left, right := newDelayedPair(3, true)
	requireThroughput(t, runExact8MiB(t, left, right, 120*time.Second))
}

func TestWB1ThroughputLoss3PctAllPacketsReorder(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput")
	}
	left, right := newDelayedPairAllPackets(3, true)
	requireThroughput(t, runExact8MiB(t, left, right, 180*time.Second))
}

func waitReady(t *testing.T, ready <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(d):
		t.Fatalf("timeout waiting for %s", what)
	}
}

func TestTwoJoinersThroughputLossOnADoesNotHoldB(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput")
	}
	key := testKey(t)
	payload := makeThroughputPayload()
	sum := sha256.Sum256(payload)

	leftA, rightA := newDelayedPair(3, true)
	leftB, rightB := newDelayedPair(0, false)
	defer leftA.Close()
	defer rightA.Close()
	defer leftB.Close()
	defer rightB.Close()

	creatorA := NewMux(key, rightA)
	creatorA.SetRoute(testSID(1), testSID(2))
	joinerA := NewMux(key, leftA)
	joinerA.SetRoute(testSID(2), testSID(1))
	creatorB := NewMux(key, rightB)
	creatorB.SetRoute(testSID(3), testSID(4))
	joinerB := NewMux(key, leftB)
	joinerB.SetRoute(testSID(4), testSID(3))

	// Separate contexts: shutting A down after B succeeds must not cancel B.
	ctxA, cancelA := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancelA()
	ctxB, cancelB := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancelB()
	go func() { _ = creatorA.Run(ctxA) }()
	go func() { _ = joinerA.Run(ctxA) }()
	go func() { _ = creatorB.Run(ctxB) }()
	go func() { _ = joinerB.Run(ctxB) }()

	acceptedA := make(chan struct{})
	acceptedB := make(chan struct{})
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	startB := make(chan time.Time, 1)

	go func() {
		_, conn, err := creatorA.Accept(ctxA)
		if err != nil {
			errA <- err
			return
		}
		close(acceptedA)
		got := make([]byte, len(payload))
		_, err = io.ReadFull(conn, got)
		errA <- err // A is lossy; transfer success is not required
	}()
	go func() {
		_, conn, err := creatorB.Accept(ctxB)
		if err != nil {
			errB <- err
			return
		}
		close(acceptedB)
		got := make([]byte, len(payload))
		t0 := time.Now()
		startB <- t0
		if _, err := io.ReadFull(conn, got); err != nil {
			errB <- err
			return
		}
		if sha256.Sum256(got) != sum {
			errB <- io.ErrUnexpectedEOF
			return
		}
		errB <- nil
	}()

	connA, err := joinerA.Dial(ctxA, "a.example:443")
	if err != nil {
		t.Fatal(err)
	}
	connB, err := joinerB.Dial(ctxB, "b.example:443")
	if err != nil {
		t.Fatal(err)
	}
	// Dial is fire-and-forget Open. Wait until Accept returned so ReadFull is
	// about to run; otherwise 8MiB fills StreamRecvCap before the reader starts
	// and B hits send-window timeout (~10s) under CPU contention.
	waitReady(t, acceptedA, 5*time.Second, "creator A accept")
	waitReady(t, acceptedB, 5*time.Second, "creator B accept")

	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		// Intentionally lossy 3%+reorder path. Write may stall or fail; that
		// must not t.Fatal or leak into later tests after B has succeeded.
		_, _ = connA.Write(payload)
	}()
	defer func() {
		cancelA()
		joinerA.Close()
		creatorA.Close()
		select {
		case <-aDone:
		case <-time.After(3 * time.Second):
			t.Log("A lossy writer still exiting after close")
		}
	}()

	writeStart := time.Now()
	if _, err := connB.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errB:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctxB.Done():
		t.Fatal("loss on A stalled B below completion")
	}
	elapsed := time.Since(writeStart)
	if t0, ok := recvTime(startB); ok && t0.Before(writeStart) {
		elapsed = time.Since(t0)
	}
	mbps := float64(len(payload)) * 8 / elapsed.Seconds() / 1e6
	t.Logf("joiner B payload %d bytes in %s = %.2f Mbps", len(payload), elapsed, mbps)
	if raceBuild {
		t.Logf("race build: B %.2f Mbps (not asserting 30.0)", mbps)
		return
	}
	if mbps < throughputMinMbps {
		t.Fatalf("joiner B %.2f Mbps, want >= %.1f (A must not hold B)", mbps, throughputMinMbps)
	}
}

func recvTime(ch <-chan time.Time) (time.Time, bool) {
	select {
	case t := <-ch:
		return t, true
	default:
		return time.Time{}, false
	}
}

func TestDelayedCarrierOneSchedulerPerDirection(t *testing.T) {
	left, right := newDelayedPair(0, false)
	defer left.Close()
	defer right.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	key := testKey(t)
	for i := 0; i < 256; i++ {
		w, err := Pack(key, Frame{Type: TypeData, Dest: testSID(1), Src: testSID(2), Payload: []byte{byte(i)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := left.Send(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	if n := left.schedN.Load(); n != 2 {
		t.Fatalf("schedulers=%d want 2 (one heap goroutine per direction)", n)
	}
}

func TestFirstTxDropPatternDeterministic(t *testing.T) {
	var drops int
	for n := 1; n <= 100; n++ {
		if firstTxDrop(n, 1) {
			drops++
		}
	}
	if drops != 1 {
		t.Fatalf("1%% drops in 100 first-tx: %d", drops)
	}
	drops = 0
	for n := 1; n <= 100; n++ {
		if firstTxDrop(n, 3) {
			drops++
		}
	}
	if drops != 3 {
		t.Fatalf("3%% drops in 100 first-tx: %d", drops)
	}
}
