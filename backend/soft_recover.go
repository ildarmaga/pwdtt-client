package backend

import "time"

// Политика soft (анти-шторм реконнектов):
//
//  1. Soft ТОЛЬКО при: CORE умер | workers=0 дольше grace | zombie
//     (воркеры >0, нет meaningful-трафика дольше stall).
//  2. Простой с живыми воркерами — НЕ soft (idle ≠ zombie).
//  3. Verify после soft: успех если workers>0; 64KiB не обязателен.
//  4. Длинный immune + cooldown между soft; лимит soft в окне.
const (
	softRecoverVerifyWait  = 45 * time.Second
	softRecoverTrafficNeed = int64(64 * 1024) // опционально: «трафик пошёл»
	softStormMax           = 3
	softStormWindow        = 10 * time.Minute
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

// shouldClearSoftPreserveOnWorkerReady — READY раньше raw_config при TunAlreadyReady.
func shouldClearSoftPreserveOnWorkerReady() bool {
	return false
}

// decideSoftApplyPath — soft apply vs full create при raw_config/wg_config.
func decideSoftApplyPath(preserve, tunAlive bool) (softApply bool) {
	return preserve && tunAlive
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

// softRecoverSucceeded — soft удался: живые воркеры ИЛИ реальный трафик.
// Idle с воркерами ≠ провал (раньше требовали 64KiB → шторм soft).
func softRecoverSucceeded(now, lastTrafficAt time.Time, bytesSinceSoft int64, workers int32) bool {
	if workers > 0 {
		return true
	}
	return softRecoverTrafficOK(now, lastTrafficAt, bytesSinceSoft)
}

// meaningfulTrafficDelta — keepalive TURN/RAW (~1 КБ/с) не считается «живым».
func meaningfulTrafficDelta(delta int64) bool {
	return delta >= trafficStallMinBytes
}

// shouldStallSoft — zombie: воркеры живы, а meaningful-трафик давно молчит.
// workers<=0 → не stall (это workersLost). Idle после soft → immune.
func shouldStallSoft(watch bool, stallDur, sinceConnect time.Duration, wasActive, stallImmune, verifyPending bool, workers int32) bool {
	if !watch || stallImmune || verifyPending {
		return false
	}
	if workers <= 0 {
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

// softStormAllows — не больше softStormMax soft за softStormWindow.
func softStormAllows(count int, windowStart, now time.Time) (allow bool, nextCount int, nextStart time.Time) {
	if windowStart.IsZero() || now.Sub(windowStart) >= softStormWindow {
		return true, 1, now
	}
	if count >= softStormMax {
		return false, count, windowStart
	}
	return true, count + 1, windowStart
}

// softTickAction — решение одного тика traffic-watch (для симуляции сценариев).
type softTickAction int

const (
	softTickNone softTickAction = iota
	softTickVerifyOK
	softTickVerifyFailSoft
	softTickStallSoft
	softTickStormBlock
)

// SoftTickInput — снимок состояния для decideSoftTick (без Orchestrator/Wails).
type SoftTickInput struct {
	Now            time.Time
	Watch          bool
	StallDur       time.Duration
	SinceConnect   time.Duration
	WasActive      bool
	StallImmune    bool
	VerifyPending  bool
	VerifyExpired  bool
	Workers        int32
	LastTrafficAt  time.Time
	BytesSinceSoft int64
	SoftBusy       bool
	CooldownActive  bool
	StormCount     int
	StormStarted   time.Time
}

// SoftTickResult — что делать и новое состояние storm-счётчика.
type SoftTickResult struct {
	Action       softTickAction
	StormCount   int
	StormStarted time.Time
}

// DecideSoftTick — единая точка политики soft на тике (тесты/симуляция).
func DecideSoftTick(in SoftTickInput) SoftTickResult {
	out := SoftTickResult{StormCount: in.StormCount, StormStarted: in.StormStarted}
	if in.SoftBusy || in.CooldownActive {
		out.Action = softTickNone
		return out
	}
	trySoft := func(action softTickAction) SoftTickResult {
		allow, next, start := softStormAllows(in.StormCount, in.StormStarted, in.Now)
		if !allow {
			out.Action = softTickStormBlock
			return out
		}
		out.Action = action
		out.StormCount = next
		out.StormStarted = start
		return out
	}
	if in.VerifyExpired {
		if softRecoverSucceeded(in.Now, in.LastTrafficAt, in.BytesSinceSoft, in.Workers) {
			out.Action = softTickVerifyOK
			return out
		}
		return trySoft(softTickVerifyFailSoft)
	}
	if shouldStallSoft(in.Watch, in.StallDur, in.SinceConnect, in.WasActive, in.StallImmune, in.VerifyPending, in.Workers) {
		return trySoft(softTickStallSoft)
	}
	out.Action = softTickNone
	return out
}

// SimTrafficNote — как noteTrafficBytes двигает lastTrafficAt (keepalive vs real).
func SimTrafficNote(lastAt time.Time, lastBytes, total int64, now time.Time) (newAt time.Time, newBytes int64, meaningful bool) {
	newBytes = lastBytes
	newAt = lastAt
	if lastAt.IsZero() {
		return now, total, false
	}
	if total < lastBytes {
		return now, total, false
	}
	if total <= lastBytes {
		return lastAt, lastBytes, false
	}
	delta := total - lastBytes
	newBytes = total
	if meaningfulTrafficDelta(delta) {
		return now, newBytes, true
	}
	return lastAt, newBytes, false
}
