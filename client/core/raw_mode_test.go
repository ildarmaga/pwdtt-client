package core

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
)

func TestRawChunkedModeSelection(t *testing.T) {
	tests := []struct {
		name      string
		tunnel    string
		transport string
		want      bool
	}{
		{name: "raw udp", tunnel: "raw", transport: "udp", want: true},
		{name: "raw tcp", tunnel: "raw", transport: "tcp", want: true},
		{name: "wg udp unchanged", tunnel: "wg", transport: "udp", want: false},
		{name: "wg tcp unchanged", tunnel: "wg", transport: "tcp", want: false},
		{name: "invalid tunnel", tunnel: "other", transport: "udp", want: false},
		{name: "invalid transport", tunnel: "raw", transport: "other", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rawChunkedEnabled(tt.tunnel, tt.transport); got != tt.want {
				t.Fatalf("rawChunkedEnabled(%q, %q)=%v want %v", tt.tunnel, tt.transport, got, tt.want)
			}
		})
	}
}

func TestRawDirectPeerAddr(t *testing.T) {
	got, err := rawDirectPeerAddr("94.242.53.211:56000", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "94.242.53.211:56003" {
		t.Fatalf("got %q", got)
	}
	got, err = rawDirectPeerAddr("94.242.53.211:56000", 56111)
	if err != nil {
		t.Fatal(err)
	}
	if got != "94.242.53.211:56111" {
		t.Fatalf("explicit got %q", got)
	}
}

func newModeTestDispatcher(t *testing.T, rawMode, rawMultipath, rawChunked bool) *Dispatcher {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(ctx, pc, NewStats(), rawMode, rawMultipath, rawChunked)
	t.Cleanup(func() {
		cancel()
		_ = pc.Close()
		d.Shutdown()
	})
	return d
}

func TestNewDispatcherModeContract(t *testing.T) {
	tests := []struct {
		name                              string
		rawMode, rawMultipath, rawChunked bool
		wantSticky, wantMP, wantChunked   bool
		wantReturnCap                     int
	}{
		{name: "raw udp", rawMode: true, rawChunked: true, wantChunked: true, wantReturnCap: rawChunkReturnChBuf},
		{name: "raw tcp", rawMode: true, rawChunked: true, wantChunked: true, wantReturnCap: rawChunkReturnChBuf},
		{name: "raw legacy sticky", rawMode: true, wantSticky: true, wantReturnCap: rawReturnChBuf},
		{name: "wg", wantReturnCap: returnChBuf},
		{name: "defensive invalid pair", rawMultipath: true, wantReturnCap: returnChBuf},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newModeTestDispatcher(t, tt.rawMode, tt.rawMultipath, tt.rawChunked)
			if d.rawSticky != tt.wantSticky || d.rawMP != tt.wantMP || d.rawChunked != tt.wantChunked {
				t.Fatalf("sticky=%v mp=%v chunked=%v", d.rawSticky, d.rawMP, d.rawChunked)
			}
			if cap(d.ReturnCh) != tt.wantReturnCap {
				t.Fatalf("ReturnCh cap=%d want %d", cap(d.ReturnCh), tt.wantReturnCap)
			}
			hasRAState := d.rawSeq != nil && d.rawReord != nil && d.rawFrameCh != nil
			if hasRAState != tt.wantMP {
				t.Fatalf("RA state=%v want %v", hasRAState, tt.wantMP)
			}
		})
	}
}

func TestDispatcherRegisterChannelPolicy(t *testing.T) {
	tests := []struct {
		name                              string
		rawMode, rawMultipath, rawChunked bool
		wantPrivate, wantPrio             bool
	}{
		{name: "raw chunked", rawMode: true, rawChunked: true, wantPrivate: true, wantPrio: true},
		{name: "raw sticky", rawMode: true, wantPrivate: true},
		{name: "wg shared"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newModeTestDispatcher(t, tt.rawMode, tt.rawMultipath, tt.rawChunked)
			slot := &WorkerSlot{ID: 1}
			d.Register(slot)
			if (slot.SendCh != nil) != tt.wantPrivate {
				t.Fatalf("private SendCh=%v want %v", slot.SendCh != nil, tt.wantPrivate)
			}
			if (slot.PrioCh != nil) != tt.wantPrio {
				t.Fatalf("PrioCh=%v want %v", slot.PrioCh != nil, tt.wantPrio)
			}
			if tt.rawChunked && cap(slot.SendCh) != rawChunkWorkerSendBuf {
				t.Fatalf("chunk SendCh cap=%d", cap(slot.SendCh))
			}
		})
	}
}

func TestRawChunkedIgnoresLegacyEnvironment(t *testing.T) {
	t.Setenv("RAW_MULTIPATH", "1")
	d := newModeTestDispatcher(t, true, false, true)
	if d.rawMP || !d.rawChunked || d.rawSticky {
		t.Fatal("legacy environment changed RAW chunk mode")
	}
}

func TestRawChunkSizeFor(t *testing.T) {
	for _, size := range []int{40, 100, 300, 700, 1100, 1200} {
		if got := rawChunkSizeFor(size); got != rawBatchSize {
			t.Fatalf("rawChunkSizeFor(%d)=%d want %d", size, got, rawBatchSize)
		}
	}
}

func TestRawChunkedPrioritizesSmallPackets(t *testing.T) {
	d := &Dispatcher{rawChunked: true, stats: NewStats()}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 2), PrioCh: make(chan []byte, 2)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 2), PrioCh: make(chan []byte, 2)}
	d.workers = []*WorkerSlot{w1, w2}
	d.dispatchChunked(make([]byte, 64))
	if len(w1.PrioCh) != 1 || len(w2.PrioCh) != 0 {
		t.Fatal("small packet must stay on current worker PrioCh")
	}
	if len(w1.SendCh)+len(w2.SendCh) != 0 {
		t.Fatal("small packet entered data queue")
	}
}

func TestRawChunkedKeepsLargePacketChunk(t *testing.T) {
	d := &Dispatcher{rawChunked: true, stats: NewStats()}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 128), PrioCh: make(chan []byte, 2)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 128), PrioCh: make(chan []byte, 2)}
	d.workers = []*WorkerSlot{w1, w2}
	for i := 0; i < 13; i++ {
		d.dispatchChunked(make([]byte, 1200))
	}
	if len(w1.SendCh) != 12 || len(w2.SendCh) != 1 {
		t.Fatalf("batch-12 split incorrectly: w1=%d w2=%d", len(w1.SendCh), len(w2.SendCh))
	}
}

func TestRawChunkedACKStaysOnSameWorkerAsData(t *testing.T) {
	d := &Dispatcher{rawChunked: true, stats: NewStats()}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 128), PrioCh: make(chan []byte, 8)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 128), PrioCh: make(chan []byte, 8)}
	d.workers = []*WorkerSlot{w1, w2}
	d.dispatchChunked(make([]byte, 1200))
	d.dispatchChunked(make([]byte, 64))
	if len(w1.SendCh) != 1 || len(w1.PrioCh) != 1 {
		t.Fatalf("ACK left the data worker: data=%d ack=%d w2ack=%d", len(w1.SendCh), len(w1.PrioCh), len(w2.PrioCh))
	}
	if len(w2.SendCh)+len(w2.PrioCh) != 0 {
		t.Fatal("ACK jumped to another worker")
	}
}

func udpGamePkt(srcHost byte, sport, dport uint16, payload int) []byte {
	if payload < 0 {
		payload = 0
	}
	pkt := make([]byte, 28+payload)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = 17
	pkt[12], pkt[13], pkt[14], pkt[15] = 10, 70, 0, srcHost
	pkt[16], pkt[17], pkt[18], pkt[19] = 1, 2, 3, 4
	binary.BigEndian.PutUint16(pkt[20:22], sport)
	binary.BigEndian.PutUint16(pkt[22:24], dport)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(8+payload))
	return pkt
}

func TestRawChunkedKeepsUDPGameSticky(t *testing.T) {
	d := newModeTestDispatcher(t, true, false, true)
	w1 := &WorkerSlot{ID: 1}
	w2 := &WorkerSlot{ID: 2}
	d.Register(w1)
	d.Register(w2)
	pkt := udpGamePkt(1, 27015, 27015, 36)
	if !rawUDPOrICMP(pkt) {
		t.Fatal("dota pkt must be UDP")
	}
	for i := 0; i < 32; i++ {
		d.dispatchSticky(append([]byte(nil), pkt...))
	}
	if len(w1.SendCh)+len(w2.SendCh) != 32 {
		t.Fatalf("want 32 delivered, got w1=%d w2=%d", len(w1.SendCh), len(w2.SendCh))
	}
	if len(w1.SendCh) > 0 && len(w2.SendCh) > 0 {
		t.Fatal("Dota UDP flow rotated across TURN workers")
	}
}

func TestRawChunkedTCPStillBatches(t *testing.T) {
	d := &Dispatcher{rawChunked: true, stats: NewStats()}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 128), PrioCh: make(chan []byte, 2)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 128), PrioCh: make(chan []byte, 2)}
	d.workers = []*WorkerSlot{w1, w2}
	pkt := tcpPkt(1, 50000, 443)
	pkt = append(pkt, make([]byte, 1160)...)
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	for i := 0; i < 13; i++ {
		d.dispatchChunked(append([]byte(nil), pkt...))
	}
	if len(w1.SendCh) != 12 || len(w2.SendCh) != 1 {
		t.Fatalf("TCP must still batch-12: w1=%d w2=%d", len(w1.SendCh), len(w2.SendCh))
	}
}

func TestRawChunkedLargeUDPBatches(t *testing.T) {
	d := &Dispatcher{rawChunked: true, stats: NewStats()}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 128), PrioCh: make(chan []byte, 8)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 128), PrioCh: make(chan []byte, 8)}
	d.workers = []*WorkerSlot{w1, w2}
	pkt := udpGamePkt(1, 443, 443, 1200)
	if rawUDPOrICMP(pkt) {
		t.Fatal("QUIC-sized UDP must not take the game-sticky path")
	}
	for i := 0; i < 13; i++ {
		d.dispatchChunked(append([]byte(nil), pkt...))
	}
	if len(w1.SendCh) != 12 || len(w2.SendCh) != 1 {
		t.Fatalf("large UDP must batch-12: w1=%d w2=%d", len(w1.SendCh), len(w2.SendCh))
	}
}

func TestRawQUICAckIsNotGameSticky(t *testing.T) {
	pkt := udpGamePkt(1, 50000, 443, 80)
	if rawUDPOrICMP(pkt) {
		t.Fatal("QUIC ACK on :443 must stay in batches, not sticky")
	}
}

func TestRawLargeGameUDPIsSticky(t *testing.T) {
	pkt := udpGamePkt(1, 50000, 27015, 1200)
	if !rawUDPOrICMP(pkt) {
		t.Fatal("Dota gameplay UDP must stay sticky even when large")
	}
}

func TestRawSTUNIsSticky(t *testing.T) {
	pkt := udpGamePkt(1, 50000, 3478, 80)
	if !rawUDPOrICMP(pkt) {
		t.Fatal("STUN 3478 must stay sticky")
	}
}

func TestRawNonSteamGameUDPIsSticky(t *testing.T) {
	pkt := udpGamePkt(1, 50000, 7777, 1200)
	if !rawUDPOrICMP(pkt) {
		t.Fatal("non-Steam game UDP must stay sticky on any port except 443")
	}
}
