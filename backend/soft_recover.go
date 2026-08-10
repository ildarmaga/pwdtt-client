package backend

import "time"

// Soft-only recovery for WDTT. Full reconnect — только смена сети (forceFull).
const (
	softRecoverVerifyWait  = 30 * time.Second
	softRecoverTrafficNeed = int64(64 * 1024) // после soft ждём реальный downlink/uplink
)

type recoverMode int

const (
	recoverSoft recoverMode = iota
	recoverFull
)

// decideRecoverMode: soft пока жив TUN; full только при forceFull (смена сети)
// или если WG интерфейса уже нет.
func decideRecoverMode(forceFull bool, softCount int, tunnelUp, wgActive bool) recoverMode {
	_ = softCount
	if forceFull {
		return recoverFull
	}
	if tunnelUp && wgActive {
		return recoverSoft
	}
	return recoverFull
}

// softRecoverTrafficOK — после soft есть свежий осмысленный трафик.
func softRecoverTrafficOK(now, lastTrafficAt time.Time, bytesSinceSoft int64) bool {
	if lastTrafficAt.IsZero() {
		return false
	}
	if now.Sub(lastTrafficAt) > trafficStallThreshold {
		return false
	}
	return bytesSinceSoft >= softRecoverTrafficNeed
}
