package wbstream

import (
	"context"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/livekit"
	"github.com/ildarmaga/whitelist-bypass/relay/tunnel"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

type peerEntry struct {
	sid       string
	identity  string
	firstSeen time.Time
	state     int32
	promoted  bool
}

const (
	TunnelModeVideo = "video"
	TunnelModeDC    = "dc"
)

type SessionConfig struct {
	RoomToken      string
	ServerURL      string
	DisplayName    string
	TunnelMode     string
	Obfuscator     *tunnel.TunnelObfuscator
	LogFn          func(string, ...any)
	SettingEngine  *webrtc.SettingEngine
	NetDialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	ResolveICEHost func(host string) (string, error)
	OnJoin         func(livekit.JoinResponse)
	VP8FPS         int
	VP8Batch       int
	RoomID         string
	AccessToken    string
	ReadBuf        int
	ScreenShare    bool // when true, publish a second VP8 track as ScreenShare and shard outbound across both
	IsJoiner       bool // when true, run the configPingPong loop; only the joiner sends VP8 config to the peer
	UseWBT         bool // KCP+smux over VP8 (WBT-1); skips relay framing and VP8 config ping-pong
}

type Session struct {
	cfg SessionConfig

	lk                 *livekit.Client
	sampleTracks       []*webrtc.TrackLocalStaticSample
	sampleTransceivers []*webrtc.RTPTransceiver

	pubReliableDC      *webrtc.DataChannel
	pubReliableDCReady bool
	subReliableDC      *webrtc.DataChannel

	vp8tun       *tunnel.MultiTrackTunnel
	dctun        *tunnel.DCTunnel
	mu           sync.Mutex
	tunFired     bool
	tunnelLost   bool // WBT: peer left SFU; allow carrier rebind + OnConnected re-fire
	remoteTracks int
	done         chan struct{}

	peersBySID            map[string]peerEntry // SID -> first-seen time + state
	kickedSIDs            map[string]bool      // SIDs we kicked; SFU may still echo them as Active until it processes the kick
	lastRebind            time.Time
	pendingSubOfferRebind bool

	configAcked     chan struct{}
	configAckedOnce sync.Once
	endOnce         sync.Once

	OnConnected     func(tunnel.DataTunnel)
	OnJoinerPresent func()
	OnTunnelLost    func()
	OnPeerRestart   func()
	// OnRemoteTrackCount fires when a remote VP8 track is added (count after ++).
	// Creator uses this to AdaptTrackCount when joiner publishes dual.
	OnRemoteTrackCount func(count int)
	// OnRemoteCandidate is forwarded from the underlying LiveKit client.
	// It fires for every trickle ICE candidate sent by the SFU, plus
	// once with target=-1 for every SDP description (carrying inline
	// candidates) before the description is applied to Pion.
	OnRemoteCandidate func(target int, candidateOrSDP string)
}

func NewSession(cfg SessionConfig) *Session {
	if cfg.LogFn == nil {
		cfg.LogFn = log.Printf
	}
	return &Session{
		cfg:         cfg,
		done:        make(chan struct{}),
		configAcked: make(chan struct{}),
	}
}

func (s *Session) MarkConfigAcked() {
	s.configAckedOnce.Do(func() {
		s.cfg.LogFn("[lk] peer acked vp8 config")
		close(s.configAcked)
	})
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) Start() error {
	s.lk = livekit.NewClient(livekit.Config{
		ServerURL:      s.cfg.ServerURL,
		Token:          s.cfg.RoomToken,
		Origin:         Origin,
		UserAgent:      common.UserAgent,
		LogFn:          s.cfg.LogFn,
		SettingEngine:  s.cfg.SettingEngine,
		NetDialContext: s.cfg.NetDialContext,
		ResolveICEHost: s.cfg.ResolveICEHost,
	})
	s.lk.OnReady = s.onLKReady
	s.lk.OnJoin = s.onLKJoin
	s.lk.OnTrack = s.onRemoteTrack
	s.lk.OnDataChannel = s.onRemoteDataChannel
	s.lk.OnPubConnected = s.startTunnel
	s.lk.OnSubICEConnected = s.onSubICEConnected
	s.lk.OnSubICEDisconnected = s.onSubICEDisconnected
	s.lk.OnSubOfferApplied = s.onSubOfferApplied
	if s.cfg.AccessToken != "" && s.cfg.RoomID != "" {
		s.lk.OnParticipantUpdate = s.onParticipantUpdate
	}
	s.lk.OnRemoteCandidate = func(target int, ic webrtc.ICECandidateInit) {
		if s.OnRemoteCandidate != nil {
			s.OnRemoteCandidate(target, ic.Candidate)
		}
	}
	s.lk.OnRemoteSDP = func(target int, _, sdp string) {
		if s.OnRemoteCandidate != nil {
			s.OnRemoteCandidate(-1, sdp)
		}
	}

	if err := s.lk.Connect(); err != nil {
		return err
	}
	go s.lk.PingLoop()
	go func() {
		if err := s.lk.ReadLoop(); err != nil {
			s.cfg.LogFn("[lk] read loop ended: %v", err)
		}
		s.endSession()
	}()
	return nil
}

func (s *Session) endSession() {
	s.endOnce.Do(func() {
		// Unblock Session.Done() FIRST. stopTunnels can stall on a stuck
		// WriteSample / pion Close after an abnormal SFU drop; if done stayed
		// closed-only-after-stop, the creator runner never rejoined and the
		// room looked "owner offline" (403 guests cannot create rooms) while
		// a zombie KCP tuneLoop kept ticking.
		close(s.done)
		s.stopTunnels()
	})
}

func (s *Session) stopTunnels() {
	s.mu.Lock()
	vp8 := s.vp8tun
	s.mu.Unlock()
	if vp8 != nil {
		vp8.Stop()
	}
}

func (s *Session) onLKJoin(join livekit.JoinResponse) {
	if s.cfg.OnJoin != nil {
		s.cfg.OnJoin(join)
	}
}

func (s *Session) onLKReady() {
	pubPC := s.lk.PubPC()
	if pubPC == nil {
		return
	}

	camID := "videochannel-" + uuid.New().String()
	trackCam, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000},
		camID, "tunnel-video-"+uuid.New().String(),
	)
	if err != nil {
		s.cfg.LogFn("[lk] create local cam track: %v", err)
		return
	}
	tracks := []*webrtc.TrackLocalStaticSample{trackCam}

	if s.cfg.ScreenShare {
		screenID := "screenchannel-" + uuid.New().String()
		trackScreen, err := webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000},
			screenID, "tunnel-screen-"+uuid.New().String(),
		)
		if err != nil {
			s.cfg.LogFn("[lk] create local screen track: %v", err)
			return
		}
		tracks = append(tracks, trackScreen)
	}

	transceivers := make([]*webrtc.RTPTransceiver, 0, len(tracks))
	for _, t := range tracks {
		trx, err := pubPC.AddTransceiverFromTrack(t,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
		if err != nil {
			s.cfg.LogFn("[lk] add transceiver: %v", err)
			return
		}
		transceivers = append(transceivers, trx)
	}

	s.mu.Lock()
	s.sampleTracks = tracks
	s.sampleTransceivers = transceivers
	s.mu.Unlock()

	ordered := true
	dc, err := pubPC.CreateDataChannel("_reliable", &webrtc.DataChannelInit{
		Ordered: &ordered,
	})
	if err != nil {
		s.cfg.LogFn("[lk] create reliable DC: %v", err)
		return
	}
	s.mu.Lock()
	s.pubReliableDC = dc
	s.mu.Unlock()
	dc.OnOpen(func() {
		s.cfg.LogFn("[lk] reliable DC open")
		s.mu.Lock()
		s.pubReliableDCReady = true
		s.mu.Unlock()
		s.maybeStartDCTunnel()
	})

	for i, t := range tracks {
		source := livekit.TrackSourceCamera
		if i > 0 {
			source = livekit.TrackSourceScreenShare
		}
		if err := s.lk.SendAddTrack(t.ID(), "videochannel",
			livekit.TrackTypeVideo, source, 1280, 720); err != nil {
			s.cfg.LogFn("[lk] send add-track: %v", err)
			return
		}
	}

	offer, err := pubPC.CreateOffer(nil)
	if err != nil {
		s.cfg.LogFn("[lk] create offer: %v", err)
		return
	}
	if err := pubPC.SetLocalDescription(offer); err != nil {
		s.cfg.LogFn("[lk] set local offer: %v", err)
		return
	}
	if err := s.lk.SendOffer(offer.SDP); err != nil {
		s.cfg.LogFn("[lk] send offer: %v", err)
		return
	}
	s.cfg.LogFn("[lk] sent publisher offer (%d bytes)", len(offer.SDP))
}

func (s *Session) startTunnel() {
	s.mu.Lock()
	if s.vp8tun != nil || len(s.sampleTracks) == 0 {
		s.mu.Unlock()
		return
	}
	subs := make([]*tunnel.VP8DataTunnel, 0, len(s.sampleTracks))
	for _, t := range s.sampleTracks {
		subs = append(subs, tunnel.NewVP8DataTunnel(t, s.cfg.Obfuscator, s.cfg.LogFn))
	}
	s.vp8tun = tunnel.NewMultiTrackTunnel(subs)
	s.vp8tun.SetOnPeerRestart(func() {
		s.cfg.LogFn("[wb] peer epoch restart (joiner reconnected obfuscation)")
		s.rearmAutoDetect()
		if s.OnPeerRestart != nil {
			s.OnPeerRestart()
		}
	})
	fps, batch := s.cfg.VP8FPS, s.cfg.VP8Batch
	if fps <= 0 {
		fps = 30
	}
	if batch <= 0 {
		batch = 64
	}
	s.vp8tun.Start(fps, batch)
	tun := s.vp8tun
	s.mu.Unlock()
	s.cfg.LogFn("[lk] vp8 tunnel writer started tracks=%d", len(subs))
	if s.cfg.IsJoiner && s.cfg.TunnelMode != TunnelModeDC && !s.cfg.UseWBT {
		go s.configPingPong(tun, len(subs))
	}

	if s.cfg.TunnelMode == TunnelModeVideo {
		if s.cfg.UseWBT && s.cfg.IsJoiner {
			// Joiner: KCP needs creator VP8 on sub track before smux can work.
			s.cfg.LogFn("[wb] joiner WBT: awaiting inbound VP8 from creator")
			return
		}
		s.fireOnConnected(tun)
		return
	}
	if s.cfg.TunnelMode == "" {
		tun.SetOnData(func(payload []byte) { s.activate(tun, payload) })
	}
}

func (s *Session) configPingPong(tun *tunnel.MultiTrackTunnel, trackCount int) {
	frame := tunnel.EncodeVP8Config(s.cfg.VP8FPS, s.cfg.VP8Batch, trackCount)
	tun.SendData(frame)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.configAcked:
			return
		case <-s.done:
			return
		case <-ticker.C:
			s.cfg.LogFn("[lk] resending vp8 config (no ack yet)")
			tun.SendData(tunnel.EncodeVP8Config(s.cfg.VP8FPS, s.cfg.VP8Batch, trackCount))
		}
	}
}

func (s *Session) maybeStartDCTunnel() {
	s.mu.Lock()
	if s.dctun != nil {
		s.mu.Unlock()
		return
	}
	pubDC := s.pubReliableDC
	subDC := s.subReliableDC
	pubReady := s.pubReliableDCReady
	s.mu.Unlock()
	if pubDC == nil || subDC == nil || !pubReady {
		return
	}
	if subDC.ReadyState() != webrtc.DataChannelStateOpen {
		return
	}
	subRaw, err := subDC.Detach()
	if err != nil {
		s.cfg.LogFn("[lk] detach sub DC: %v", err)
		return
	}
	pubRaw, err := pubDC.Detach()
	if err != nil {
		s.cfg.LogFn("[lk] detach pub DC: %v", err)
		return
	}
	readWrapped := newDataPacketWrapper(subRaw, livekit.DataPacketKindReliable)
	writeWrapped := newDataPacketWrapper(pubRaw, livekit.DataPacketKindReliable)
	readBuf := s.cfg.ReadBuf
	if readBuf == 0 {
		readBuf = common.DCBufSize
	}
	dctun := tunnel.NewChunkedDCTunnelFromRaw(readWrapped, writeWrapped, s.cfg.Obfuscator, readBuf, s.cfg.LogFn)
	if dctun == nil {
		return
	}
	s.mu.Lock()
	s.dctun = dctun
	s.mu.Unlock()
	s.cfg.LogFn("[lk] dc tunnel ready (pub+sub _reliable)")

	if s.cfg.TunnelMode == TunnelModeDC {
		s.fireOnConnected(dctun)
		return
	}
	if s.cfg.TunnelMode == "" {
		dctun.SetOnData(func(payload []byte) { s.activate(dctun, payload) })
	}
}

func (s *Session) onSubICEConnected() {
	if s.cfg.UseWBT && s.cfg.IsJoiner {
		go func() {
			t0 := time.Now()
			// Let a few RTP frames arrive on sub before KCP attaches to vp8tun.
			s.cfg.LogFn("[wb] joiner WBT: sub ICE connected — RTP settle 400ms…")
			time.Sleep(400 * time.Millisecond)
			want := 1
			if s.cfg.ScreenShare {
				want = 2
				// Wait until creator scaled up to 2 VP8 (seq-prefix + reorder),
				// otherwise dual joiner drops unprefixed frames → remote not ready.
				s.cfg.LogFn("[wb] joiner WBT: waiting for creator dual tracks (max 8s)…")
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					s.mu.Lock()
					n := s.remoteTracks
					s.mu.Unlock()
					if n >= want {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
				s.mu.Lock()
				n := s.remoteTracks
				s.mu.Unlock()
				if n < want {
					s.cfg.LogFn("[wb] joiner WBT: dual wanted but creator tracks=%d after %v — starting anyway", n, time.Since(t0).Round(time.Millisecond))
				} else {
					s.cfg.LogFn("[wb] joiner WBT: creator dual ready (remote tracks=%d) after %v", n, time.Since(t0).Round(time.Millisecond))
					// Creator AdaptTrackCount + RestartLink needs a beat before KCP.
					s.cfg.LogFn("[wb] joiner WBT: post-dual settle 700ms…")
					time.Sleep(700 * time.Millisecond)
				}
			}
			s.mu.Lock()
			tun := s.vp8tun
			need := tun != nil && (!s.tunFired || s.tunnelLost)
			s.mu.Unlock()
			if need {
				s.cfg.LogFn("[wb] joiner WBT: sub ICE ready — starting KCP (elapsed %v)", time.Since(t0).Round(time.Millisecond))
				s.fireOnConnected(tun)
			}
		}()
		return
	}
	s.maybeRebindTunnel("sub ICE connected")
}

func (s *Session) onSubICEDisconnected() {
	// Creator publishes the room; once its subscriber ICE is down the SFU
	// path is dead regardless of a stale peersBySID "Active" entry. Waiting
	// for active==0 left rooms zombie after WS/ICE drops (field: no
	// "joiner offline", KCP kept ticking, joiner got 403 guests cannot create).
	if s.cfg.UseWBT && !s.cfg.IsJoiner {
		s.maybeTunnelLost("sub ICE down")
		return
	}
	s.checkJoinerAbsent("sub ICE down")
}

func (s *Session) onSubOfferApplied() {
	s.mu.Lock()
	if s.cfg.UseWBT && s.cfg.IsJoiner && !s.tunFired {
		s.pendingSubOfferRebind = true
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.maybeRebindTunnel("sub offer")
}

func (s *Session) maybeRebindTunnel(reason string) {
	s.mu.Lock()
	tun := s.vp8tun
	rebind := s.tunFired
	lost := s.tunnelLost
	// Debounce only the live-ICE skip path — never block tunnelLost recovery.
	if reason == "sub offer" && !lost && time.Since(s.lastRebind) < 5*time.Second {
		s.mu.Unlock()
		return
	}
	if tun != nil && rebind && !lost {
		s.lastRebind = time.Now()
	}
	s.mu.Unlock()
	if tun == nil {
		return
	}
	if s.cfg.UseWBT {
		// ICE renegotiation (sub offer / sub ICE) keeps the same MultiTrackTunnel;
		// KCP OnData stays attached. Forcing SwapTunnel→RestartLink here races
		// joiner smux client vs creator smux server → "remote not ready: invalid
		// protocol" and soft-rebind loops. Full rebind only after tunnelLost
		// (peer leave / ICE down) or watchdog/peer-epoch RestartLink.
		if !lost {
			if rebind {
				s.cfg.LogFn("[wb] %s — WBT carrier alive, skip KCP/smux restart", reason)
			}
			return
		}
		s.cfg.LogFn("[wb] %s — WBT carrier rebind after tunnel lost", reason)
		s.fireOnConnected(tun)
		return
	}
	if !rebind {
		return
	}
	s.cfg.LogFn("[wb] %s — rebinding WBT tunnel", reason)
	s.notifyTunnelReady(tun)
}

func (s *Session) notifyTunnelReady(tun tunnel.DataTunnel) {
	if s.OnConnected != nil {
		s.OnConnected(tun)
	}
}

func (s *Session) fireOnConnected(tun tunnel.DataTunnel) {
	s.mu.Lock()
	s.tunFired = true
	s.tunnelLost = false
	pending := s.pendingSubOfferRebind
	s.pendingSubOfferRebind = false
	s.mu.Unlock()
	s.notifyTunnelReady(tun)
	// Deferred sub-offer used to call maybeRebindTunnel again → double SwapTunnel
	// while smux was still settling (remote not ready / closed pipe flood).
	// First onConnected already has the live VP8 carrier.
	if pending && s.cfg.UseWBT && s.cfg.IsJoiner {
		s.cfg.LogFn("[wb] sub offer (deferred) — skipped second rebind (carrier already live)")
	}
}

func (s *Session) activate(tun tunnel.DataTunnel, payload []byte) {
	s.mu.Lock()
	if s.tunFired {
		s.mu.Unlock()
		return
	}
	s.tunFired = true
	s.mu.Unlock()
	s.cfg.LogFn("[lk] auto-detected active tunnel: %T", tun)
	if s.OnConnected != nil {
		s.OnConnected(tun)
	}
	var fwd func([]byte)
	switch v := tun.(type) {
	case *tunnel.DCTunnel:
		fwd = v.OnData()
	case *tunnel.MultiTrackTunnel:
		// MultiTrackTunnel does not expose a readable OnData hook; the trigger
		// payload is dropped here. Next frame will arrive via the SetOnData
		// callback OnConnected wires up.
	}
	if fwd != nil {
		fwd(payload)
	}
}

func (s *Session) currentVP8Tun() *tunnel.MultiTrackTunnel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vp8tun
}

// AdaptTrackCount matches joiner dual-track: trackCount=1 → single VP8,
// trackCount=2 → camera+screenshare. Called from RelayBridge MsgConfig.
func (s *Session) AdaptTrackCount(peerCount int) {
	if peerCount < 1 {
		peerCount = 1
	}
	if peerCount > 2 {
		peerCount = 2 // RelayBridge dual is at most 2 tracks
	}
	pubPC := s.lk.PubPC()
	if pubPC == nil {
		return
	}
	s.mu.Lock()
	current := len(s.sampleTracks)
	s.mu.Unlock()
	dual := peerCount >= 2
	if peerCount == current {
		s.cfg.LogFn("[lk] adapt-track-count: joiner dual=%v tracks=%d (already matched)", dual, current)
		return
	}
	if pubPC.ConnectionState() == webrtc.PeerConnectionStateClosed ||
		pubPC.ConnectionState() == webrtc.PeerConnectionStateFailed {
		s.cfg.LogFn("[lk] adapt-track-count: pub PC %s, skip (joiner dual=%v want=%d have=%d)",
			pubPC.ConnectionState(), dual, peerCount, current)
		return
	}

	if peerCount < current {
		// Keep both VP8 writers. Soft-shrink left an orphaned SFU screenshare
		// transceiver (no renegotiate) and Windows SOCKS sessions often saw
		// CONNECT OK + first send but almost no downlink (ya.ru/Telegram dead
		// while pings looked fine). SendData is camera-only anyway.
		s.cfg.LogFn("[lk] adapt-track-count: joiner dual=false want=%d have=%d — keep writers (no soft-shrink)", peerCount, current)
		return
	}

	// Prefer starting with ScreenShare=true (2 tracks). Mid-session
	// renegotiation while SOCKS is live has broken WB SFU data path.
	s.cfg.LogFn("[lk] adapt-track-count: joiner dual=true want=%d have=%d — scale-up (may renegotiate)", peerCount, current)
	for i := current; i < peerCount; i++ {
		if !s.addPublisherTrack(pubPC, i) {
			return
		}
	}
	// So onRemoteTrack no longer drains peer's 2nd VP8 (needed for WBT reorder).
	s.mu.Lock()
	s.cfg.ScreenShare = true
	s.mu.Unlock()
	offer, err := pubPC.CreateOffer(nil)
	if err != nil {
		s.cfg.LogFn("[lk] adapt-track-count: create offer: %v", err)
		return
	}
	if err := pubPC.SetLocalDescription(offer); err != nil {
		s.cfg.LogFn("[lk] adapt-track-count: set local offer: %v", err)
		return
	}
	if err := s.lk.SendOffer(offer.SDP); err != nil {
		s.cfg.LogFn("[lk] adapt-track-count: send offer: %v", err)
		return
	}
	s.cfg.LogFn("[lk] adapt-track-count: renegotiation offer sent (%d bytes), tracks=%d (dual=true)", len(offer.SDP), peerCount)
}

// softRemovePublisherTrack drops the trailing VP8 writer from the multi-track
// tunnel and bookkeeping, but leaves the SFU transceiver in place (no Stop /
// renegotiate). Hard remove broke pub PC on WB Stream.
func (s *Session) softRemovePublisherTrack() bool {
	s.mu.Lock()
	if len(s.sampleTracks) <= 1 {
		s.mu.Unlock()
		s.cfg.LogFn("[lk] adapt-track-count: refusing to remove cam slot")
		return false
	}
	s.sampleTracks = s.sampleTracks[:len(s.sampleTracks)-1]
	if len(s.sampleTransceivers) > 1 {
		s.sampleTransceivers = s.sampleTransceivers[:len(s.sampleTransceivers)-1]
	}
	vp8 := s.vp8tun
	s.mu.Unlock()
	if vp8 != nil {
		vp8.RemoveLastSubTunnel()
	}
	return true
}

func (s *Session) addPublisherTrack(pubPC *webrtc.PeerConnection, slot int) bool {
	labelPrefix := "screenchannel-"
	streamPrefix := "tunnel-screen-"
	source := livekit.TrackSourceScreenShare
	if slot == 0 {
		labelPrefix = "videochannel-"
		streamPrefix = "tunnel-video-"
		source = livekit.TrackSourceCamera
	}
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000},
		labelPrefix+uuid.New().String(), streamPrefix+uuid.New().String(),
	)
	if err != nil {
		s.cfg.LogFn("[lk] adapt-track-count: new track slot=%d: %v", slot, err)
		return false
	}
	trx, err := pubPC.AddTransceiverFromTrack(track,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		s.cfg.LogFn("[lk] adapt-track-count: add transceiver slot=%d: %v", slot, err)
		return false
	}
	if err := s.lk.SendAddTrack(track.ID(), "videochannel",
		livekit.TrackTypeVideo, source, 1280, 720); err != nil {
		s.cfg.LogFn("[lk] adapt-track-count: send add-track slot=%d: %v", slot, err)
		return false
	}
	s.mu.Lock()
	s.sampleTracks = append(s.sampleTracks, track)
	s.sampleTransceivers = append(s.sampleTransceivers, trx)
	vp8 := s.vp8tun
	s.mu.Unlock()
	if vp8 != nil {
		vp8.AddSubTunnel(tunnel.NewVP8DataTunnel(track, s.cfg.Obfuscator, s.cfg.LogFn))
	}
	return true
}

func (s *Session) rearmAutoDetect() {
	if s.cfg.TunnelMode != "" {
		return
	}
	s.mu.Lock()
	s.tunFired = false
	vp8 := s.vp8tun
	dc := s.dctun
	s.mu.Unlock()
	if vp8 != nil {
		vp8.SetOnData(func(payload []byte) { s.activate(vp8, payload) })
	}
	if dc != nil {
		dc.SetOnData(func(payload []byte) { s.activate(dc, payload) })
	}
}

func (s *Session) signalJoinerPresent(reason string) {
	if s.OnJoinerPresent != nil {
		s.cfg.LogFn("[wb] joiner present: %s", reason)
		s.OnJoinerPresent()
	}
}

func (s *Session) maybeTunnelLost(reason string) {
	if s.cfg.UseWBT {
		s.mu.Lock()
		s.tunFired = false
		s.tunnelLost = true
		s.mu.Unlock()
	}
	if s.OnTunnelLost != nil {
		s.cfg.LogFn("[wb] tunnel lost: %s", reason)
		s.OnTunnelLost()
	}
}

func (s *Session) onRemoteTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	if track.Codec().MimeType != webrtc.MimeTypeVP8 {
		go func() {
			buf := make([]byte, common.UDPBufSize)
			for {
				if _, _, err := track.Read(buf); err != nil {
					return
				}
			}
		}()
		return
	}
	s.mu.Lock()
	s.remoteTracks++
	count := s.remoteTracks
	cb := s.OnRemoteTrackCount
	s.mu.Unlock()
	if cb != nil {
		cb(count)
	}
	s.mu.Lock()
	wantDual := s.cfg.ScreenShare
	s.mu.Unlock()
	if shouldDrainRemoteVP8(wantDual, count) {
		if !wantDual {
			s.cfg.LogFn("[wb] remote VP8 track #%d — drain only (single-track mode)", count)
		} else {
			s.cfg.LogFn("[wb] remote VP8 track #%d — drain (dual already has 2)", count)
		}
		go drainRemoteTrack(track)
		return
	}
	if count > 1 {
		s.cfg.LogFn("[wb] remote VP8 track #%d (dual-track ok)", count)
	}
	go s.readVP8Track(track)
}

// shouldDrainRemoteVP8 reports whether an inbound VP8 track must be discarded
// instead of feeding the tunnel. Dual accepts at most 2; extras from ICE
// renegotiation (#3+) must not share HandleFrame with the data path.
func shouldDrainRemoteVP8(wantDual bool, count int) bool {
	if count <= 1 {
		return false
	}
	if !wantDual {
		return true
	}
	return count > 2
}

func drainRemoteTrack(track *webrtc.TrackRemote) {
	buf := make([]byte, common.RTPBufSize)
	for {
		if _, _, err := track.Read(buf); err != nil {
			return
		}
	}
}

func (s *Session) readVP8Track(track *webrtc.TrackRemote) {
	var vp8Pkt codecs.VP8Packet
	var frameBuf []byte
	var lastSeq uint16
	var haveLastSeq bool
	frameValid := false
	signaledJoiner := false
	buf := make([]byte, common.RTPBufSize)
	for {
		n, _, err := track.Read(buf)
		if err != nil {
			return
		}
		pkt := &rtp.Packet{}
		if pkt.Unmarshal(buf[:n]) != nil {
			continue
		}
		if haveLastSeq && pkt.SequenceNumber != lastSeq+1 {
			frameValid = false
			frameBuf = frameBuf[:0]
		}
		lastSeq = pkt.SequenceNumber
		haveLastSeq = true

		vp8Payload, err := vp8Pkt.Unmarshal(pkt.Payload)
		if err != nil {
			frameValid = false
			frameBuf = frameBuf[:0]
			continue
		}
		if vp8Pkt.S == 1 {
			frameBuf = frameBuf[:0]
			frameValid = true
		}
		if !frameValid {
			continue
		}
		frameBuf = append(frameBuf, vp8Payload...)
		if !pkt.Marker {
			continue
		}
		if !signaledJoiner {
			signaledJoiner = true
			s.signalJoinerPresent("VP8 media")
		}

		tun := s.currentVP8Tun()
		if tun != nil {
			tun.HandleFrame(frameBuf)
		}
		frameBuf = frameBuf[:0]
		frameValid = false
	}
}

func (s *Session) onRemoteDataChannel(dc *webrtc.DataChannel) {
	s.cfg.LogFn("[lk] remote DC label=%s id=%v", dc.Label(), dc.ID())
	if dc.Label() != "_reliable" {
		return
	}
	s.mu.Lock()
	s.subReliableDC = dc
	s.mu.Unlock()
	dc.OnOpen(func() {
		s.cfg.LogFn("[lk] remote _reliable DC open")
		s.maybeStartDCTunnel()
	})
}

func (s *Session) checkJoinerAbsent(reason string) {
	s.mu.Lock()
	active := 0
	for _, e := range s.peersBySID {
		if e.state == livekit.ParticipantStateActive {
			active++
		}
	}
	s.mu.Unlock()
	if active == 0 {
		s.maybeTunnelLost(reason)
	}
}

func (s *Session) onParticipantUpdate(updates []livekit.ParticipantInfo) {
	selfSID := s.lk.Join().ParticipantSID

	s.mu.Lock()
	if s.peersBySID == nil {
		s.peersBySID = make(map[string]peerEntry)
	}
	newcomerSIDs := make(map[string]bool)
	canPromote := s.cfg.AccessToken != "" && s.cfg.RoomID != ""
	joinerPresent := false
	joinerLeft := false
	for _, p := range updates {
		if p.SID == "" || p.SID == selfSID {
			continue
		}
		if p.State == livekit.ParticipantStateDisconnected {
			delete(s.peersBySID, p.SID)
			delete(s.kickedSIDs, p.SID)
			joinerLeft = true
			continue
		}
		if s.kickedSIDs[p.SID] {
			continue
		}
		entry, ok := s.peersBySID[p.SID]
		if !ok {
			entry = peerEntry{sid: p.SID, identity: p.Identity, firstSeen: time.Now()}
			newcomerSIDs[p.SID] = true
		}
		if p.Identity != "" {
			entry.identity = p.Identity
		}
		entry.state = p.State
		s.peersBySID[p.SID] = entry
		if p.State == livekit.ParticipantStateActive {
			joinerPresent = true
		}
	}

	var stale []peerEntry
	staleSIDs := make(map[string]bool)
	if len(newcomerSIDs) > 0 {
		for _, e := range s.peersBySID {
			if e.state == livekit.ParticipantStateActive && !newcomerSIDs[e.sid] {
				stale = append(stale, e)
				staleSIDs[e.sid] = true
			}
		}
	}

	var toPromote []peerEntry
	if canPromote {
		for sid, entry := range s.peersBySID {
			if staleSIDs[sid] {
				continue
			}
			if !entry.promoted && entry.state == livekit.ParticipantStateActive && entry.identity != "" {
				entry.promoted = true
				s.peersBySID[sid] = entry
				toPromote = append(toPromote, entry)
			}
		}
	}
	s.mu.Unlock()

	if joinerPresent {
		s.signalJoinerPresent("participant active")
	}
	if joinerLeft {
		s.checkJoinerAbsent("peer disconnected")
	}

	for _, e := range toPromote {
		go s.promotePeer(e.sid, e.identity)
	}

	if len(stale) == 0 {
		return
	}

	for _, e := range stale {
		if e.identity == "" {
			continue
		}
		if err := KickParticipant(http.DefaultClient, s.cfg.AccessToken, s.cfg.RoomID, e.identity); err != nil {
			s.cfg.LogFn("[wb] kick failed identity=%s: %v", e.identity, err)
			continue
		}
		s.cfg.LogFn("[wb] kicked stale peer identity=%s sid=%s", e.identity, e.sid)
		s.mu.Lock()
		delete(s.peersBySID, e.sid)
		if s.kickedSIDs == nil {
			s.kickedSIDs = make(map[string]bool)
		}
		s.kickedSIDs[e.sid] = true
		s.mu.Unlock()
	}
}

func (s *Session) promotePeer(sid, identity string) {
	if err := SetParticipantPermissions(http.DefaultClient, s.cfg.AccessToken, s.cfg.RoomID, identity, ModeratorPermissions); err != nil {
		s.cfg.LogFn("[wb] promote failed identity=%s: %v", identity, err)
		s.mu.Lock()
		if entry, ok := s.peersBySID[sid]; ok {
			entry.promoted = false
			s.peersBySID[sid] = entry
		}
		s.mu.Unlock()
		return
	}
	s.cfg.LogFn("[wb] promoted to moderator identity=%s sid=%s", identity, sid)
}

func (s *Session) Close() {
	if s.lk != nil {
		s.lk.Close()
	}
	s.endSession()
}
