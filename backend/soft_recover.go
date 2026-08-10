package backend

import "time"

// Soft-only recovery for WDTT. Full reconnect — только смена сети (forceFull).
const (
	softRecoverVerifyWait  = 30 * time.Second
	softRecoverTrafficNeed = int64(64 * 1024) // после soft ждём реальный downlink/uplink
)

// softRecoverBusy — нельзя рвать текущий soft повторным SoftReconnect
// (иначе context canceled посреди TURN/VK auth).
func softRecoverBusy(vkDirect bool, until, now time.Time) bool {
	if vkDirect {
		return true
	}
	return !until.IsZero() && now.Before(until)
}

// shouldSkipFinishSoftRecover — старая сессия ещё гасится, Start ещё не был.
func shouldSkipFinishSoftRecover(softSwap, softCoreStarted bool) bool {
	return softSwap && !softCoreStarted
}

// shouldAutoSoftOnCoreEnd — CORE умер сам, TUN ещё жив → soft вместо «Отключено».
func shouldAutoSoftOnCoreEnd(preserve, swap, suppress, tunnelUp, wgActive bool) bool {
	return !preserve && !swap && !suppress && tunnelUp && wgActive
}

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

// meaningfulTrafficDelta — keepalive TURN/RAW (~1 КБ/с) не считается «живым».
// Иначе zombie (18 воркеров, download 0) никогда не триггерит soft.
func meaningfulTrafficDelta(delta int64) bool {
	return delta >= trafficStallMinBytes
}

// shouldStallSoft — soft при залипании; первые секунды после connect ждём
// реальный трафик, не soft'им на пустом старте.
// stallImmune / verifyPending — только что был soft, ждём verify или паузу idle.
func shouldStallSoft(watch bool, stallDur, sinceConnect time.Duration, wasActive, stallImmune, verifyPending bool) bool {
	if !watch || stallImmune || verifyPending {
		return false
	}
	if stallDur < trafficStallThreshold {
		return false
	}
	if wasActive {
		return true
	}
	return sinceConnect >= trafficStallStartupGrace
}
