package wbtunnel

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDualFingerprintConcurrentSOCKS reproduces the Telegram storm on a
// dual-track (SubTunnelCount=2) carrier with camera-only KCP (no reorder/seq).
// All connects must get a ready ack well under the 25s "remote not ready" budget.
func TestDualFingerprintConcurrentSOCKS(t *testing.T) {
	upstream := startAcceptCloseServer(t)
	host, portStr, err := net.SplitHostPort(upstream.String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	a, b := newPipePair()
	creatorTun := &dualPipe{pipeTunnel: a, subs: 2}
	joinerTun := &dualPipe{pipeTunnel: b, subs: 2}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	defer creator.Close()

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	defer joiner.Close()

	waitKCP(t)

	const n = 48
	var ok atomic.Int32
	var fail atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	start := time.Now()
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			j := joiner
			j.mu.Lock()
			sess := j.smuxSess
			j.mu.Unlock()
			if sess == nil {
				fail.Add(1)
				return
			}
			stream, err := sess.OpenStream()
			if err != nil {
				fail.Add(1)
				return
			}
			defer stream.Close()

			req, _ := json.Marshal(ConnectRequest{Cmd: connectCommand, Addr: host, Port: port})
			_ = stream.SetDeadline(time.Now().Add(8 * time.Second))
			if _, err := stream.Write(req); err != nil {
				fail.Add(1)
				return
			}
			ack := make([]byte, 1)
			if _, err := io.ReadFull(stream, ack); err != nil {
				fail.Add(1)
				return
			}
			if ack[0] != 0x00 {
				fail.Add(1)
				return
			}
			ok.Add(1)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	if fail.Load() != 0 {
		t.Fatalf("dual SOCKS storm: ok=%d fail=%d elapsed=%v (remote not ready regression)",
			ok.Load(), fail.Load(), elapsed.Round(time.Millisecond))
	}
	if ok.Load() != n {
		t.Fatalf("ok=%d want %d", ok.Load(), n)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("dual SOCKS storm too slow: %v (carrier/smux wedge)", elapsed)
	}
	t.Logf("dual fingerprint SOCKS×%d ok in %v", n, elapsed.Round(time.Millisecond))
}
