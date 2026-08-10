package backend

import "time"

// Soft-recover escalation for WDTT (WG/RAW via Orchestrator).
// SoftReconnect keeps wg-turn; after a server restart that is often a zombie
// path (workers up, data dead). One soft attempt, then full reconnect.
const (
	softRecoverMax         = 1
	softRecoverVerifyWait  = 45 * time.Second
	softRecoverTrafficNeed = int64(2048) // meaningful progress after soft
)

type recoverMode int

const (
	recoverSoft recoverMode = iota
	recoverFull
)

// decideRecoverMode выбирает soft vs полный reconnect.
// forceFull — смена сети / эскалация после неудачного soft.
func decideRecoverMode(forceFull bool, softCount int, tunnelUp, wgActive bool) recoverMode {
	if forceFull {
		return recoverFull
	}
	if softCount >= softRecoverMax {
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
