package core

import (
	"context"
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
	got, err := rawDirectPeerAddr("94.242.53.211:56000")
	if err != nil {
		t.Fatal(err)
	}
	if got != "94.242.53.211:56003" {
		t.Fatalf("got %q", got)
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
