package wbstream

import (
	"testing"

	"github.com/ildarmaga/whitelist-bypass/relay/tunnel"
)

func TestMaybeRebindTunnelSkipsLiveICE(t *testing.T) {
	var connected int
	s := NewSession(SessionConfig{
		UseWBT:   true,
		IsJoiner: true,
		LogFn:    func(string, ...any) {},
	})
	s.OnConnected = func(tunnel.DataTunnel) { connected++ }
	s.vp8tun = tunnel.NewMultiTrackTunnel(nil)
	s.tunFired = true
	s.tunnelLost = false

	s.maybeRebindTunnel("sub offer")
	if connected != 0 {
		t.Fatalf("live ICE renegotiation must not OnConnected/SwapTunnel, got %d", connected)
	}

	s.maybeRebindTunnel("sub ICE connected")
	if connected != 0 {
		t.Fatalf("live sub ICE must not OnConnected, got %d", connected)
	}

	s.tunnelLost = true
	s.maybeRebindTunnel("sub offer")
	if connected != 1 {
		t.Fatalf("after tunnelLost want OnConnected once, got %d", connected)
	}
	if s.tunnelLost {
		t.Fatal("fireOnConnected should clear tunnelLost")
	}
}

func TestMaybeRebindTunnelNonWBTStillRebinds(t *testing.T) {
	var connected int
	s := NewSession(SessionConfig{
		UseWBT: false,
		LogFn:  func(string, ...any) {},
	})
	s.OnConnected = func(tunnel.DataTunnel) { connected++ }
	s.vp8tun = tunnel.NewMultiTrackTunnel(nil)
	s.tunFired = true

	s.maybeRebindTunnel("sub offer")
	if connected != 1 {
		t.Fatalf("legacy path should notifyTunnelReady, got %d", connected)
	}
}
