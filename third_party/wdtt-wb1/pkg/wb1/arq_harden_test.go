package wb1

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDupAckDoesNotRefreshLastProgress(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.sendUnacked = 1
	m.sendNext = 5
	m.cwnd = 64
	m.rto = minRTO
	stale := time.Now().Add(-time.Second)
	m.lastProgress = stale
	for seq := uint32(1); seq < 5; seq++ {
		m.sendBuf[seq] = &sendSlot{
			seq:     seq,
			wire:    []byte{1},
			sentAt:  time.Now().Add(-time.Second),
			firstAt: time.Now(),
		}
	}

	dup := Frame{Type: TypeAck, Payload: packAckPayload(1, 0, 32)}
	m.handleAck(context.Background(), dup)
	if !m.lastProgress.Equal(stale) {
		t.Fatalf("duplicate ACK refreshed lastProgress; stall/cwnd would be suppressed")
	}
	if m.cwnd != 64 {
		t.Fatalf("dup ACK changed cwnd to %d", m.cwnd)
	}
	if _, ok := m.sendBuf[1]; !ok {
		t.Fatal("dup ACK must not remove outstanding slots")
	}

	if !m.retransmitDue(context.Background()) {
		t.Fatal("retransmitDue")
	}
	if m.cwnd != initialFlight {
		t.Fatalf("true stall after dup ACK must still cut cwnd, got %d", m.cwnd)
	}

	before := m.lastProgress
	progress := Frame{Type: TypeAck, Payload: packAckPayload(3, 0, 32)}
	m.handleAck(context.Background(), progress)
	if !m.lastProgress.After(before) {
		t.Fatal("ACK that frees slots must update lastProgress")
	}
	if _, ok := m.sendBuf[1]; ok {
		t.Fatal("cumulative ACK should free seq 1")
	}
}

func TestSACKNewlyUpdatesProgressDupDoesNot(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.sendUnacked = 1
	m.sendNext = 4
	stale := time.Now().Add(-500 * time.Millisecond)
	m.lastProgress = stale
	m.sendBuf[1] = &sendSlot{seq: 1, wire: []byte{1}}
	m.sendBuf[2] = &sendSlot{seq: 2, wire: []byte{1}}
	m.sendBuf[3] = &sendSlot{seq: 3, wire: []byte{1}}

	// SACK bit 0 => seq cum+1 = 2; replay of same SACK later is newly=0
	m.handleAck(context.Background(), Frame{Type: TypeAck, Payload: packAckPayload(1, 1, 32)})
	if !m.lastProgress.After(stale) {
		t.Fatal("SACK that removes a slot must update lastProgress")
	}
	after := m.lastProgress
	time.Sleep(2 * time.Millisecond)
	m.handleAck(context.Background(), Frame{Type: TypeAck, Payload: packAckPayload(1, 1, 32)})
	if !m.lastProgress.Equal(after) {
		t.Fatal("replayed SACK must not refresh lastProgress")
	}
}

func TestPackFailureRewindsWhenContiguous(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.SetRoute(testSID(1), testSID(2))
	m.packHook = func(Frame) ([]byte, error) {
		return nil, errors.New("injected pack fail")
	}
	err := m.sendReliable(context.Background(), Frame{Type: TypeData, Payload: []byte("x")})
	if err == nil {
		t.Fatal("want pack error")
	}
	if m.closed.Load() {
		t.Fatal("contiguous rewind must not close mux")
	}
	m.mu.Lock()
	next, nbuf := m.sendNext, len(m.sendBuf)
	m.mu.Unlock()
	if next != 1 {
		t.Fatalf("sendNext=%d, want rewind to 1", next)
	}
	if nbuf != 0 {
		t.Fatalf("placeholder left in sendBuf (%d)", nbuf)
	}
	select {
	case <-m.sendWait:
	default:
		t.Fatal("contiguous pack rewind must wakeSend")
	}
}

func TestPackFailureConcurrentSeqHoleFailsClosed(t *testing.T) {
	var sent atomic.Int32
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error {
		sent.Add(1)
		return nil
	}})
	m.SetRoute(testSID(1), testSID(2))

	var hold sync.WaitGroup
	hold.Add(1)
	m.packHook = func(f Frame) ([]byte, error) {
		if f.Type == TypeData && f.Seq == 1 {
			hold.Wait()
			return nil, errors.New("injected pack fail")
		}
		return packWithAEAD(m.aead, f)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.sendReliable(context.Background(), Frame{Type: TypeData, Payload: []byte("a")})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		m.mu.Lock()
		n := m.sendNext
		m.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("seq 1 never reserved")
		}
		time.Sleep(time.Millisecond)
	}

	second := make(chan error, 1)
	go func() {
		second <- m.sendReliable(context.Background(), Frame{Type: TypeData, Payload: []byte("b")})
	}()
	for {
		m.mu.Lock()
		n := m.sendNext
		m.mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("seq 2 never reserved")
		}
		time.Sleep(time.Millisecond)
	}
	hold.Done()

	err := <-errCh
	if err == nil {
		t.Fatal("first pack should fail")
	}
	if !m.closed.Load() {
		t.Fatal("unrecoverable seq hole must fail-closed")
	}
	select {
	case <-m.closeCh:
	case <-time.After(time.Second):
		t.Fatal("waiters not woken")
	}
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("second sender stuck")
	}
}

func TestRetransmitDueKeepsMuxIfAcksStillMove(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.rto = minRTO
	now := time.Now()
	m.lastProgress = now
	m.sendUnacked = 1
	m.sendNext = 4
	old := now.Add(-arqStallTimeout - time.Second)
	m.sendBuf[1] = &sendSlot{seq: 1, wire: []byte{1}, sentAt: old, firstAt: old, retries: maxARQRetries + 1}
	m.sendBuf[2] = &sendSlot{seq: 2, wire: []byte{1}, sentAt: now.Add(-time.Second), firstAt: now.Add(-time.Second)}
	m.sendBuf[3] = &sendSlot{seq: 3, wire: []byte{1}, sentAt: now.Add(-time.Second), firstAt: now.Add(-time.Second)}
	if !m.retransmitDue(context.Background()) {
		t.Fatal("one stale segment while ACKs still move must not close mux")
	}
}

func TestRetransmitDueClosesWhenNoAckProgress(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.rto = minRTO
	m.lastProgress = time.Now().Add(-arqStallTimeout - time.Second)
	m.sendUnacked = 1
	m.sendNext = 2
	old := time.Now().Add(-arqStallTimeout - time.Second)
	m.sendBuf[1] = &sendSlot{
		seq:     1,
		wire:    []byte{1},
		sentAt:  old,
		firstAt: old,
	}
	if m.retransmitDue(context.Background()) {
		t.Fatal("no ACK progress with in-flight data must still close mux")
	}
}

func TestRetransmitDueKeepsMuxAfterIdleResume(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.rto = minRTO
	now := time.Now()
	m.lastProgress = now.Add(-arqStallTimeout - time.Second)
	m.sendUnacked = 1
	m.sendNext = 2
	m.sendBuf[1] = &sendSlot{seq: 1, wire: []byte{1}, sentAt: now, firstAt: now}
	if !m.retransmitDue(context.Background()) {
		t.Fatal("first send after idle must not close mux")
	}
}
