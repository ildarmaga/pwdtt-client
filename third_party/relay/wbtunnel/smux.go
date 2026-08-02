package wbtunnel

import (
	"time"

	"github.com/xtaci/smux"
)

func smuxConfig() *smux.Config {
	cfg := smux.DefaultConfig()
	cfg.Version = 2
	cfg.KeepAliveDisabled = false
	// Mobile NAT: detect dead mapping faster without hammering the radio every 10s.
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.KeepAliveTimeout = 60 * time.Second
	cfg.MaxFrameSize = 32 * 1024
	// Flow-control windows gate single-connection throughput: speed ≤ window / RTT.
	// The effective RTT through WebRTC+KCP is far higher than raw ping (jitter buffer
	// + KCP interval + flush), so 512KB throttled bulk to ~20-30 Mbps and collapsed
	// on RTT spikes. Larger windows lift the ceiling without hurting page loads
	// (smux still schedules streams fairly within the session buffer).
	cfg.MaxReceiveBuffer = 8 * 1024 * 1024
	cfg.MaxStreamBuffer = 2 * 1024 * 1024
	return cfg
}
