package wb1

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	wantAckPrefixLen     = 14
	wantSackExtraWords   = 15
	wantSackBits         = 1024
	wantInitialFlight    = 32
	wantMaxRexmitPerTick = 64
	wantMinRTO           = 120 * time.Millisecond
	wantFastDupFloor     = 20 * time.Millisecond
)

func TestAckPayloadPrefixAndExtendedLength(t *testing.T) {
	p := packAckPayload(0x11223344, 0x5566778899aabbcc, 32)
	if len(p) < wantAckPrefixLen {
		t.Fatalf("ack too short: %d", len(p))
	}
	if binary.BigEndian.Uint32(p[0:4]) != 0x11223344 {
		t.Fatal("cum moved")
	}
	if binary.BigEndian.Uint64(p[4:12]) != 0x5566778899aabbcc {
		t.Fatal("sack64 moved")
	}
	if binary.BigEndian.Uint16(p[12:14]) != 32 {
		t.Fatal("recvWnd moved")
	}
	if len(p) < wantAckPrefixLen+wantSackExtraWords*8 {
		t.Fatalf("extended SACK missing: len=%d want >= %d", len(p), wantAckPrefixLen+wantSackExtraWords*8)
	}
}

func TestUnpackAckAcceptsOld14AndExtended(t *testing.T) {
	old := make([]byte, wantAckPrefixLen)
	binary.BigEndian.PutUint32(old[0:4], 99)
	binary.BigEndian.PutUint64(old[4:12], 3)
	binary.BigEndian.PutUint16(old[12:14], 32)
	cum, sack, wnd, ok := unpackAckPayload(old)
	if !ok || cum != 99 || sack != 3 || wnd != 32 {
		t.Fatalf("old 14-byte ACK: cum=%d sack=%d wnd=%d ok=%v", cum, sack, wnd, ok)
	}

	ext := make([]byte, wantAckPrefixLen+wantSackExtraWords*8)
	copy(ext, old)
	binary.BigEndian.PutUint64(ext[14:22], 1) // bit 64
	cum, sack, wnd, ok = unpackAckPayload(ext)
	if !ok || cum != 99 || sack != 3 || wnd != 32 {
		t.Fatalf("extended prefix: cum=%d sack=%d wnd=%d ok=%v", cum, sack, wnd, ok)
	}
	if !ackWireBit(ext, 64) {
		t.Fatal("extended bit 64 not present on wire")
	}
	if ackWireBit(ext, 65) {
		t.Fatal("bit 65 should be clear")
	}
}

func TestUnpackAckRejectsShorterThanPrefix(t *testing.T) {
	if _, _, _, ok := unpackAckPayload(make([]byte, 13)); ok {
		t.Fatal("13-byte ACK must be rejected")
	}
}

func ackWireBit(p []byte, bit uint32) bool {
	if bit < 64 {
		if len(p) < 12 {
			return false
		}
		return binary.BigEndian.Uint64(p[4:12])&(uint64(1)<<bit) != 0
	}
	off := 14 + int((bit/64)-1)*8
	if len(p) < off+8 {
		return false
	}
	return binary.BigEndian.Uint64(p[off:off+8])&(uint64(1)<<(bit%64)) != 0
}

func TestExtendedSACKCoverageNear1024(t *testing.T) {
	key := testKey(t)
	var got []byte
	m := NewMux(key, &countCarrier{send: func(p []byte) error {
		typ, _, _, ok := PeekRoute(p)
		if ok && typ == TypeAck {
			f, err := Unpack(key, p)
			if err == nil {
				got = append([]byte(nil), f.Payload...)
			}
		}
		return nil
	}})
	m.SetRoute(testSID(1), testSID(2))
	m.recvNext = 10
	m.recvBuf[10+100] = Frame{Seq: 110}
	m.recvBuf[10+1000] = Frame{Seq: 1010}
	m.sendAck(context.Background())
	if len(got) < wantAckPrefixLen+wantSackExtraWords*8 {
		t.Fatalf("ACK payload len %d, want extended", len(got))
	}
	if binary.BigEndian.Uint32(got[0:4]) != 10 {
		t.Fatalf("cum %d", binary.BigEndian.Uint32(got[0:4]))
	}
	// bit i => seq cum+1+i; seq 110 => i=99; seq 1010 => i=999
	if !ackWireBit(got, 99) {
		t.Fatal("SACK missing bit 99 (seq cum+100)")
	}
	if 999 >= wantSackBits {
		t.Fatal("test bit 999 must be inside 1024-bit SACK")
	}
	if !ackWireBit(got, 999) {
		t.Fatal("SACK missing bit 999 (seq cum+1000), coverage must reach near 1024")
	}
}

func TestInitialFlight32UntilPeerAck(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.sendUnacked = 1
	m.sendNext = 1 + uint32(wantInitialFlight)
	m.mu.Lock()
	full := m.sendWindowFullLocked()
	m.mu.Unlock()
	if !full {
		t.Fatal("before any peer ACK, inflight 32 must fill the initial flight")
	}
	m.sendNext = 1 + uint32(wantInitialFlight) - 1
	m.mu.Lock()
	full = m.sendWindowFullLocked()
	m.mu.Unlock()
	if full {
		t.Fatal("inflight 31 must still send before ACK")
	}
}

func TestSendWindowPeerWnd32CapsNewSender(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.gotPeerWnd = true
	m.peerRecvWnd = 32
	m.sendUnacked = 1
	m.sendNext = 1 + 32
	m.mu.Lock()
	full := m.sendWindowFullLocked()
	m.mu.Unlock()
	if !full {
		t.Fatal("new sender must stay at 32 when old peer advertises 32")
	}
	m.sendNext = 1 + 31
	m.mu.Lock()
	full = m.sendWindowFullLocked()
	m.mu.Unlock()
	if full {
		t.Fatal("31 < peer wnd 32 should not be full")
	}
}

func TestFlightGrowsPast32AfterValidAck(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	joiner := NewMux(key, left)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, c)
	}()
	conn, err := joiner.Dial(ctx, "grow.example:1")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := conn.Write(bytes.Repeat([]byte("g"), 96*MaxPayload)); err != nil {
		t.Fatal(err)
	}
	joiner.mu.Lock()
	cwnd := joiner.cwnd
	got := joiner.gotPeerWnd
	joiner.mu.Unlock()
	if !got {
		t.Fatal("expected peer ACK")
	}
	if cwnd <= wantInitialFlight {
		t.Fatalf("after peer ACK, cwnd %d, want > %d", cwnd, wantInitialFlight)
	}
}

func TestMinRTOIs120ms(t *testing.T) {
	if minRTO != wantMinRTO {
		t.Fatalf("minRTO=%s, want %s", minRTO, wantMinRTO)
	}
	if initialRTO != 200*time.Millisecond {
		t.Fatalf("initialRTO=%s, want 200ms", initialRTO)
	}
	if retransmitTick != 10*time.Millisecond || ackDelay != 10*time.Millisecond {
		t.Fatalf("tick=%s ackDelay=%s", retransmitTick, ackDelay)
	}
	if fastDupFloor != wantFastDupFloor {
		t.Fatalf("fastDupFloor=%s, want %s", fastDupFloor, wantFastDupFloor)
	}
}

func TestRTOCutsCwndOncePerRecovery(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.cwnd = 256
	m.rto = minRTO
	m.lastProgress = time.Now().Add(-time.Second)
	past := time.Now().Add(-time.Second)
	const stuffed = 200
	for i := uint32(1); i <= stuffed; i++ {
		m.sendBuf[i] = &sendSlot{seq: i, wire: []byte{1, 2, 3}, sentAt: past, firstAt: past}
	}
	m.sendUnacked = 1
	m.sendNext = stuffed + 1
	ctx := context.Background()

	if !m.retransmitDue(ctx) {
		t.Fatal("first tick")
	}
	if m.cwnd != 128 {
		t.Fatalf("first stall cut cwnd=%d, want 128", m.cwnd)
	}
	if !m.inRecovery {
		t.Fatal("inRecovery must be set after first stall cut")
	}
	if !m.retransmitDue(ctx) {
		t.Fatal("second tick")
	}
	if m.cwnd != 128 {
		t.Fatalf("second tick in same episode cut cwnd to %d, want 128", m.cwnd)
	}
	if !m.retransmitDue(ctx) {
		t.Fatal("third tick")
	}
	if m.cwnd != 128 {
		t.Fatalf("third tick cut cwnd to %d (want stay 128, not 64/32)", m.cwnd)
	}

	m.handleAck(ctx, Frame{Type: TypeAck, Payload: packAckPayload(1, 0, 1024)})
	if !m.inRecovery {
		t.Fatal("dup ACK (newly=0) must not clear inRecovery")
	}

	m.handleAck(ctx, Frame{Type: TypeAck, Payload: packAckPayload(10, 0, 1024)})
	if m.inRecovery {
		t.Fatal("newly>0 must clear inRecovery")
	}
	afterAck := m.cwnd
	if afterAck < 128 {
		t.Fatalf("progress ACK should not shrink cwnd, got %d", afterAck)
	}

	m.lastProgress = time.Now().Add(-time.Second)
	for i := uint32(10); i <= 20; i++ {
		if slot, ok := m.sendBuf[i]; ok {
			slot.sentAt = past
			slot.retries = 0
		} else {
			m.sendBuf[i] = &sendSlot{seq: i, wire: []byte{1}, sentAt: past, firstAt: past}
		}
	}
	if !m.retransmitDue(ctx) {
		t.Fatal("post-ACK stall")
	}
	want := afterAck / 2
	if want < initialFlight {
		want = initialFlight
	}
	if m.cwnd != want {
		t.Fatalf("after recovery ACK, later stall cut cwnd=%d, want %d", m.cwnd, want)
	}
	if !m.inRecovery {
		t.Fatal("later stall must enter recovery again")
	}
}

func TestFastRetransmitRetryGapIsFloorNotSRTT(t *testing.T) {
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error { return nil }})
	m.srtt = 72 * time.Millisecond
	m.sendUnacked = 1
	m.sendNext = 3
	orig := time.Now().Add(-50 * time.Millisecond)
	hole := &sendSlot{seq: 1, wire: []byte{1}, sentAt: orig, firstAt: orig}
	m.sendBuf[1] = hole
	// seq 2 is SACKed and already removed, as handleAck does before collectFastRetransmit.

	var bits sackBitmap
	bits.set(0) // seq = cum+1+0 = 2 SACKed; seq 1 is the hole
	now := time.Now()

	m.mu.Lock()
	fast := m.collectFastRetransmitLocked(now, 1, bits)
	m.mu.Unlock()
	if len(fast) != 1 || fast[0].seq != 1 {
		t.Fatalf("first fast RTX: %d slots", len(fast))
	}
	if !hole.sentAt.Equal(orig) {
		t.Fatal("fast RTX must not reset sentAt (RTO must keep original)")
	}
	if hole.lastFast.IsZero() || !hole.karn {
		t.Fatal("fast RTX must set lastFast and karn")
	}

	m.mu.Lock()
	again := m.collectFastRetransmitLocked(now.Add(5*time.Millisecond), 1, bits)
	m.mu.Unlock()
	if len(again) != 0 {
		t.Fatal("must not storm within fastDupFloor")
	}

	m.mu.Lock()
	retry := m.collectFastRetransmitLocked(now.Add(wantFastDupFloor+time.Millisecond), 1, bits)
	m.mu.Unlock()
	if len(retry) != 1 {
		t.Fatalf("dropped hole must be eligible again after ~20ms (not srtt=72ms), got %d", len(retry))
	}
	if !hole.sentAt.Equal(orig) {
		t.Fatal("RTO sentAt must remain original after fast RTX retry")
	}
}

func TestRTORetransmitMax64NoBurst(t *testing.T) {
	var n atomic.Int32
	m := NewMux(testKey(t), &countCarrier{send: func([]byte) error {
		n.Add(1)
		return nil
	}})
	m.SetRoute(testSID(1), testSID(2))
	m.rto = minRTO
	past := time.Now().Add(-time.Second)
	const stuffed = 200
	for i := uint32(1); i <= stuffed; i++ {
		m.sendBuf[i] = &sendSlot{seq: i, wire: []byte{1, 2, 3}, sentAt: past, firstAt: past}
	}
	m.sendUnacked = 1
	m.sendNext = stuffed + 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !m.retransmitDue(ctx) {
		t.Fatal("retransmitDue closed mux")
	}
	got := n.Load()
	if got > int32(wantMaxRexmitPerTick) {
		t.Fatalf("RTO burst %d packets in one tick, want <= %d", got, wantMaxRexmitPerTick)
	}
	if got < 1 {
		t.Fatal("RTO sent nothing")
	}
}

func TestFastRetransmitHoleBeforeRTO(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	var target atomic.Uint32
	var dropped, rexmit atomic.Bool
	var dropAt, rexmitAt atomic.Int64
	filt := &filterCarrier{inner: left, drop: func(p []byte) bool {
		typ, _, _, ok := PeekRoute(p)
		if !ok || typ != TypeData {
			return false
		}
		f, err := Unpack(key, p)
		if err != nil {
			return false
		}
		want := target.Load()
		if want == 0 || f.Seq != want {
			return false
		}
		if dropped.CompareAndSwap(false, true) {
			dropAt.Store(time.Now().UnixNano())
			return true
		}
		if rexmit.CompareAndSwap(false, true) {
			rexmitAt.Store(time.Now().UnixNano())
		}
		return false
	}}
	joiner := NewMux(key, filt)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, c)
	}()
	conn, err := joiner.Dial(ctx, "fast.example:1")
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("f"), MaxPayload)
	if _, err := conn.Write(chunk); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	joiner.mu.Lock()
	target.Store(joiner.sendNext)
	joiner.mu.Unlock()
	if _, err := conn.Write(chunk); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if _, err := conn.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(minRTO + 100*time.Millisecond)
	for time.Now().Before(deadline) {
		if rexmit.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !dropped.Load() {
		t.Fatal("did not drop target first transmission")
	}
	if !rexmit.Load() {
		t.Fatal("hole was not retransmitted")
	}
	d := time.Duration(rexmitAt.Load() - dropAt.Load())
	if d >= minRTO {
		t.Fatalf("fast retransmit took %s, want < minRTO %s", d, minRTO)
	}
}

func TestFastRetransmitMultipleHolesBoundedNoStorm(t *testing.T) {
	key := testKey(t)
	left, right := newCarrierPair()
	var dropSet syncMap32
	var muN atomic.Int32
	counts := make([]int32, 64)
	filt := &filterCarrier{inner: left, drop: func(p []byte) bool {
		typ, _, _, ok := PeekRoute(p)
		if !ok || typ != TypeData {
			return false
		}
		f, err := Unpack(key, p)
		if err != nil {
			return false
		}
		if dropSet.has(f.Seq) {
			if dropSet.addOnce(f.Seq) {
				return true
			}
			idx := dropSet.index(f.Seq)
			if idx >= 0 && idx < len(counts) {
				counts[idx]++
			}
			muN.Add(1)
		}
		return false
	}}
	joiner := NewMux(key, filt)
	joiner.SetRoute(testSID(1), testSID(2))
	creator := NewMux(key, right)
	creator.SetRoute(testSID(2), testSID(1))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = joiner.Run(ctx) }()
	go func() { _ = creator.Run(ctx) }()
	defer left.Close()
	defer right.Close()

	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, c)
	}()
	conn, err := joiner.Dial(ctx, "holes.example:1")
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("h"), MaxPayload)
	if _, err := conn.Write(chunk); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	joiner.mu.Lock()
	base := joiner.sendNext
	joiner.mu.Unlock()
	holes := []uint32{base, base + 2, base + 5, base + 8, base + 11}
	for _, s := range holes {
		dropSet.add(s)
	}
	if _, err := conn.Write(bytes.Repeat([]byte("h"), 24*MaxPayload)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(minRTO / 2)
	if muN.Load() < 1 {
		t.Fatal("expected fast retransmit of holes before minRTO")
	}
	for i := range holes {
		if counts[i] > 8 {
			t.Fatalf("seq storm: hole %d rexmit=%d", holes[i], counts[i])
		}
	}
	if muN.Load() > int32(wantMaxRexmitPerTick*4) {
		t.Fatalf("retransmit storm: %d", muN.Load())
	}
}

func TestSendRecvBufNeverExceedWindow(t *testing.T) {
	left, right := newShakyPair(12, 5)
	joiner, creator, cancel := startMuxPair(t, left, right)
	defer cancel()
	defer left.Close()
	defer right.Close()

	const n = 256 * 1024
	payload := bytes.Repeat([]byte("b"), n)
	ctx, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()
	errCh := make(chan error, 1)
	go func() {
		_, c, err := creator.Accept(ctx)
		if err != nil {
			errCh <- err
			return
		}
		buf := make([]byte, n)
		_, err = io.ReadFull(c, buf)
		errCh <- err
	}()
	conn, err := joiner.Dial(ctx, "bound.example:1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := conn.Write(payload)
		done <- err
	}()
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		joiner.mu.Lock()
		sb := len(joiner.sendBuf)
		joiner.mu.Unlock()
		creator.mu.Lock()
		rb := len(creator.recvBuf)
		creator.mu.Unlock()
		if sb > ARQWindow {
			t.Fatalf("sendBuf %d > ARQWindow %d", sb, ARQWindow)
		}
		if rb > ARQWindow {
			t.Fatalf("recvBuf %d > ARQWindow %d", rb, ARQWindow)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			deadline = time.Time{}
		default:
			time.Sleep(2 * time.Millisecond)
		}
		if deadline.IsZero() {
			break
		}
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("receiver timeout")
	}
}

type syncMap32 struct {
	mu   sync.Mutex
	vals []uint32
	ok   [8]bool
}

func (s *syncMap32) add(v uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.vals) >= len(s.ok) {
		return
	}
	s.vals = append(s.vals, v)
}

func (s *syncMap32) has(v uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.vals {
		if x == v {
			return true
		}
	}
	return false
}

func (s *syncMap32) indexLocked(v uint32) int {
	for i, x := range s.vals {
		if x == v {
			return i
		}
	}
	return -1
}

func (s *syncMap32) addOnce(v uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(v)
	if i < 0 || i >= len(s.ok) {
		return false
	}
	if s.ok[i] {
		return false
	}
	s.ok[i] = true
	return true
}

func (s *syncMap32) index(v uint32) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.indexLocked(v)
}
