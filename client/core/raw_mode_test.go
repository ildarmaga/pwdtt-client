package core

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
)

func TestRawChunkedModeSelection(t *testing.T) {
	t.Setenv("RAW_CHUNKED", "")
	tests := []struct {
		name      string
		tunnel    string
		transport string
		want      bool
	}{
		{name: "raw udp default sticky", tunnel: "raw", transport: "udp", want: false},
		{name: "raw tcp default sticky", tunnel: "raw", transport: "tcp", want: false},
		{name: "wg udp unchanged", tunnel: "wg", transport: "udp", want: false},
		{name: "wg tcp unchanged", tunnel: "wg", transport: "tcp", want: false},
		{name: "invalid tunnel", tunnel: "other", transport: "udp", want: false},
		{name: "invalid transport", tunnel: "raw", transport: "other", want: false},
		{name: "empty tunnel", tunnel: "", transport: "udp", want: false},
		{name: "empty transport", tunnel: "raw", transport: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rawChunkedEnabled(tt.tunnel, tt.transport); got != tt.want {
				t.Fatalf("rawChunkedEnabled(%q, %q)=%v want %v", tt.tunnel, tt.transport, got, tt.want)
			}
		})
	}
}

func TestRawChunkedEnabledExperimentalEnv(t *testing.T) {
	t.Setenv("RAW_CHUNKED", "1")
	if !rawChunkedEnabled("raw", "udp") || !rawChunkedEnabled("raw", "tcp") {
		t.Fatal("RAW_CHUNKED=1 must enable experimental CHUNK1 for RAW UDP/TCP")
	}
	if rawChunkedEnabled("wg", "udp") {
		t.Fatal("WG must stay off CHUNK1 even with RAW_CHUNKED=1")
	}
	if rawChunkedEnabled("raw", "other") {
		t.Fatal("invalid RAW transport must stay off CHUNK1")
	}
	t.Setenv("RAW_CHUNKED", "0")
	if rawChunkedEnabled("raw", "udp") {
		t.Fatal("RAW_CHUNKED=0 must keep default sticky")
	}
}

func TestRawDefaultGateDoesNotRequestChunk1(t *testing.T) {
	t.Setenv("RAW_CHUNKED", "")
	ch := make(chan string, 1)
	g := newWGConfigGate(ch, "raw", 1280, "", rawChunkedEnabled("raw", "udp"))
	if g.requireChunk {
		t.Fatal("default RAW gate must not request CHUNK1")
	}
	t.Setenv("RAW_CHUNKED", "1")
	g = newWGConfigGate(ch, "raw", 1280, "", rawChunkedEnabled("raw", "udp"))
	if !g.requireChunk {
		t.Fatal("experimental RAW_CHUNKED=1 must request CHUNK1")
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
		{name: "raw udp default sticky", rawMode: true, wantSticky: true, wantReturnCap: rawReturnChBuf},
		{name: "raw tcp default sticky", rawMode: true, wantSticky: true, wantReturnCap: rawReturnChBuf},
		{name: "raw experimental chunked", rawMode: true, rawChunked: true, wantChunked: true, wantReturnCap: rawChunkReturnChBuf},
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
		{name: "raw default sticky", rawMode: true, wantPrivate: true},
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

func TestDefaultRawDispatchSticksSmallGameUDP(t *testing.T) {
	t.Setenv("RAW_CHUNKED", "")
	chunked := rawChunkedEnabled("raw", "udp")
	mp := rawMultipathEnabled("raw", "udp")
	d := newModeTestDispatcher(t, true, mp, chunked)
	if d.rawChunked || d.rawMP || !d.rawSticky {
		t.Fatalf("default RAW must be sticky, got sticky=%v mp=%v chunked=%v", d.rawSticky, d.rawMP, d.rawChunked)
	}
	w1 := &WorkerSlot{ID: 1}
	w2 := &WorkerSlot{ID: 2}
	d.Register(w1)
	d.Register(w2)
	pkt := udpGamePkt(1, 27015, 27015, 36)
	for i := 0; i < 32; i++ {
		d.dispatchSticky(append([]byte(nil), pkt...))
	}
	if len(w1.SendCh)+len(w2.SendCh) != 32 {
		t.Fatalf("want 32 delivered, got w1=%d w2=%d", len(w1.SendCh), len(w2.SendCh))
	}
	if len(w1.SendCh) > 0 && len(w2.SendCh) > 0 {
		t.Fatal("Dota/CS2-sized UDP flow rotated across TURN workers")
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
	tests := []struct {
		size, want int
	}{
		{1200, 64}, {1100, 24}, {700, 8}, {300, 3}, {100, 1},
	}
	for _, tt := range tests {
		if got := rawChunkSizeFor(tt.size); got != tt.want {
			t.Fatalf("rawChunkSizeFor(%d)=%d want %d", tt.size, got, tt.want)
		}
	}
}

func TestRawChunkedPrioritizesSmallPackets(t *testing.T) {
	d := &Dispatcher{rawChunked: true, stats: NewStats()}
	w1 := &WorkerSlot{ID: 1, SendCh: make(chan []byte, 2), PrioCh: make(chan []byte, 2)}
	w2 := &WorkerSlot{ID: 2, SendCh: make(chan []byte, 2), PrioCh: make(chan []byte, 2)}
	d.workers = []*WorkerSlot{w1, w2}
	d.dispatchChunked(make([]byte, 64))
	if len(w1.PrioCh)+len(w2.PrioCh) != 1 {
		t.Fatal("small packet did not use priority channel")
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
	for i := 0; i < 65; i++ {
		d.dispatchChunked(make([]byte, 1200))
	}
	if len(w1.SendCh) != 64 || len(w2.SendCh) != 1 {
		t.Fatalf("large chunk split incorrectly: w1=%d w2=%d", len(w1.SendCh), len(w2.SendCh))
	}
}
