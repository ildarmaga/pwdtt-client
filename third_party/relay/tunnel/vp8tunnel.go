package tunnel

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	defaultVP8FPS       = 30
	defaultVP8Batch     = 64
	keepaliveIdlePeriod = 100 * time.Millisecond
	forceKeyframePeriod = 2 * time.Second
	sendQueueDepth      = 4096
	// maxCoalescePlain is the max multiplex plaintext per VP8 sample (RTP MTU budget).
	maxCoalescePlain = 1400
)

type VP8DataTunnel struct {
	track     *webrtc.TrackLocalStaticSample
	logFn     func(string, ...any)
	obf       *TunnelObfuscator
	stopCh    chan struct{}
	sendQueue chan []byte
	cfgChan   chan struct{}

	stopOnce sync.Once
	running  atomic.Bool

	cfgMu sync.Mutex
	fps   int
	batch int

	sentFrames atomic.Uint64
	recvFrames atomic.Uint64

	lastKeyframeMu sync.Mutex
	lastKeyframe   time.Time

	lastSlowLogNs atomic.Int64

	writeMu sync.Mutex

	pendingMu       sync.Mutex
	pendingCoalesce []byte

	OnData        func([]byte)
	OnClose       func()
	OnPeerRestart func()
}

func (t *VP8DataTunnel) SetOnData(fn func([]byte))  { t.OnData = fn }
func (t *VP8DataTunnel) SetOnClose(fn func())       { t.OnClose = fn }
func (t *VP8DataTunnel) SetOnPeerRestart(fn func()) { t.OnPeerRestart = fn }

func NewVP8DataTunnel(track *webrtc.TrackLocalStaticSample, obf *TunnelObfuscator, logFn func(string, ...any)) *VP8DataTunnel {
	return &VP8DataTunnel{
		track:     track,
		obf:       obf,
		logFn:     logFn,
		stopCh:    make(chan struct{}),
		sendQueue: make(chan []byte, sendQueueDepth),
		cfgChan:   make(chan struct{}, 1),
		fps:       defaultVP8FPS,
		batch:     defaultVP8Batch,
	}
}

func (t *VP8DataTunnel) Reconfigure(fps, batch int) {
	if fps <= 0 && batch <= 0 {
		return
	}
	t.cfgMu.Lock()
	changed := false
	if fps > 0 && t.fps != fps {
		t.fps = fps
		changed = true
	}
	if batch > 0 && t.batch != batch {
		t.batch = batch
		changed = true
	}
	newFPS, newBatch := t.fps, t.batch
	t.cfgMu.Unlock()
	if !changed {
		return
	}
	t.logFn("vp8tunnel: reconfigure fps=%d batch=%d", newFPS, newBatch)
	select {
	case t.cfgChan <- struct{}{}:
	default:
	}
}

func (t *VP8DataTunnel) FPS() int {
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.fps
}

func (t *VP8DataTunnel) Batch() int {
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	return t.batch
}

func (t *VP8DataTunnel) SendData(data []byte) {
	if len(data) == 0 {
		return
	}
	select {
	case t.sendQueue <- data:
	case <-t.stopCh:
	}
}

// SendUrgent bypasses the coalesce queue for latency-sensitive payloads (WBT/KCP).
func (t *VP8DataTunnel) SendUrgent(data []byte) {
	if len(data) == 0 || !t.running.Load() {
		return
	}
	sampleInterval, _, _, _ := t.currentIntervals()
	sample := t.obf.EncodeData(data)
	t.writeSample(sample, sampleInterval)
}

func (t *VP8DataTunnel) Start(fps, batch int) {
	t.cfgMu.Lock()
	if fps > 0 {
		t.fps = fps
	}
	if batch > 0 {
		t.batch = batch
	}
	t.cfgMu.Unlock()
	if !t.running.CompareAndSwap(false, true) {
		return
	}
	go t.writerLoop()
}

func (t *VP8DataTunnel) Stop() {
	if !t.running.CompareAndSwap(true, false) {
		return
	}
	t.stopOnce.Do(func() { close(t.stopCh) })
	if t.OnClose != nil {
		t.OnClose()
	}
}

func (t *VP8DataTunnel) currentIntervals() (sampleInterval time.Duration, keepaliveEvery, fps, batch int) {
	t.cfgMu.Lock()
	fps = t.fps
	batch = t.batch
	t.cfgMu.Unlock()

	if fps <= 0 {
		fps = defaultVP8FPS
	}
	if batch <= 0 {
		batch = defaultVP8Batch
	}

	frameInterval := time.Second / time.Duration(fps)
	sampleInterval = frameInterval
	if batch > 1 {
		sampleInterval = frameInterval / time.Duration(batch)
	}
	if sampleInterval <= 0 {
		sampleInterval = time.Millisecond
	}

	keepaliveEvery = int(keepaliveIdlePeriod / sampleInterval)
	if keepaliveEvery < 1 {
		keepaliveEvery = 1
	}
	return
}

// coalesceFromQueue merges up to maxPackets multiplex frames into one VP8
// plaintext blob. DecodeFrames on the peer already handles multiple frames
// per decrypt — same idea as olcrtc vp8channel batchSample.
func (t *VP8DataTunnel) coalesceFromQueue(maxPackets int) []byte {
	if maxPackets < 1 {
		maxPackets = 1
	}
	var first []byte
	t.pendingMu.Lock()
	if len(t.pendingCoalesce) > 0 {
		first = t.pendingCoalesce
		t.pendingCoalesce = nil
	}
	t.pendingMu.Unlock()
	if first == nil {
		select {
		case first = <-t.sendQueue:
		default:
			return nil
		}
	}
	out := make([]byte, 0, len(first)+maxCoalescePlain)
	out = append(out, first...)
	for packets := 1; packets < maxPackets; packets++ {
		select {
		case data := <-t.sendQueue:
			if len(out)+len(data) > maxCoalescePlain {
				t.pendingMu.Lock()
				t.pendingCoalesce = data
				t.pendingMu.Unlock()
				return out
			}
			out = append(out, data...)
		default:
			return out
		}
	}
	return out
}

func (t *VP8DataTunnel) maybeForceKeyframe() bool {
	t.lastKeyframeMu.Lock()
	defer t.lastKeyframeMu.Unlock()
	if time.Since(t.lastKeyframe) < forceKeyframePeriod {
		return false
	}
	sample := t.obf.EncodeKeepalive()
	if sample == nil {
		return false
	}
	if err := t.track.WriteSample(media.Sample{Data: sample, Duration: time.Second / 30}); err != nil {
		t.logFn("vp8tunnel: force keyframe WriteSample error: %v", err)
		return false
	}
	t.lastKeyframe = time.Now()
	t.sentFrames.Add(1)
	return true
}

func (t *VP8DataTunnel) markKeyframeSent() {
	t.lastKeyframeMu.Lock()
	t.lastKeyframe = time.Now()
	t.lastKeyframeMu.Unlock()
}

// vp8WriteSlowThreshold: WriteSample is fully synchronous down through SRTP →
// DTLS → ICE → the kernel/TURN send buffer, holding writeMu the whole time. A
// slow WriteSample therefore freezes the ENTIRE VP8 writer for that track —
// keepalives and every KCP SendUrgent frame (ACKs included). Field wedges showed
// carrier RTT ballooning to 15s+ with almost no app data in flight, i.e. the
// stall lives here, below KCP. Log it so the field logs pinpoint the transport
// stall (and its duration) directly instead of only the downstream KCP timeouts.
const vp8WriteSlowThreshold = 150 * time.Millisecond

func (t *VP8DataTunnel) writeSample(sample []byte, dur time.Duration) {
	if sample == nil {
		return
	}
	t.writeMu.Lock()
	start := time.Now()
	err := t.track.WriteSample(media.Sample{Data: sample, Duration: dur})
	elapsed := time.Since(start)
	t.writeMu.Unlock()
	if err != nil {
		t.logFn("vp8tunnel: WriteSample error: %v", err)
		return
	}
	if elapsed > vp8WriteSlowThreshold {
		// Rate-limit to at most one line per 500ms so a sustained stall does not
		// flood the log while still surfacing the total blocked time.
		nowNs := start.Add(elapsed).UnixNano()
		last := t.lastSlowLogNs.Load()
		if nowNs-last > int64(500*time.Millisecond) && t.lastSlowLogNs.CompareAndSwap(last, nowNs) {
			t.logFn("vp8tunnel: SLOW WriteSample %s — transport send backpressure (carrier stall)",
				elapsed.Round(time.Millisecond))
		}
	}
	n := t.sentFrames.Add(1)
	_ = n // stats only; no per-frame log (floods UI at high traffic)
}

func (t *VP8DataTunnel) writerLoop() {
	for {
		sampleInterval, keepaliveEvery, fps, batch := t.currentIntervals()
		t.logFn("vp8tunnel: writer (re)started fps=%d batch=%d sampleInterval=%s keepaliveEvery=%d coalesce=%d",
			fps, batch, sampleInterval, keepaliveEvery, maxCoalescePlain)

		ticker := time.NewTicker(sampleInterval)
		idleTicks := 0
		reconfigure := false

		for !reconfigure {
			select {
			case <-t.stopCh:
				ticker.Stop()
				return
			case <-t.cfgChan:
				reconfigure = true
			case <-ticker.C:
				// olcrtc-style: inject decodable keyframe during idle bulk so SFU keeps forwarding.
				// Skip under active queue — keyframes steal VP8 bandwidth mid-download.
				if len(t.sendQueue) == 0 {
					t.pendingMu.Lock()
					idle := len(t.pendingCoalesce) == 0
					t.pendingMu.Unlock()
					if idle {
						t.maybeForceKeyframe()
					}
				}

				coalesced := t.coalesceFromQueue(batch)
				if len(coalesced) > 0 {
					idleTicks = 0
					sample := t.obf.EncodeData(coalesced)
					t.writeSample(sample, sampleInterval)
					continue
				}

				idleTicks++
				if idleTicks < keepaliveEvery {
					continue
				}
				idleTicks = 0
				sample := t.obf.EncodeKeepalive()
				t.writeSample(sample, sampleInterval)
				t.markKeyframeSent()
			}
		}
		ticker.Stop()
	}
}

func (t *VP8DataTunnel) HandleFrame(frame []byte) {
	res := t.obf.Decode(frame)
	if !res.HasFrame {
		return
	}
	if res.SelfEcho {
		return
	}
	if res.PeerRestart {
		t.logFn("vp8tunnel: peer restart detected, new epoch=0x%08x", res.PeerEpoch)
		if t.OnPeerRestart != nil {
			t.OnPeerRestart()
		}
	}
	if res.Keepalive || len(res.Payload) == 0 {
		return
	}
	n := t.recvFrames.Add(1)
	_ = n
	if t.OnData != nil {
		t.OnData(res.Payload)
	}
}
