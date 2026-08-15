package backend

import (
	"sync/atomic"
	"time"
)

// Политика soft (анти-шторм + быстрый recover):
//
//  1. Soft ТОЛЬКО при: CORE умер | workers=0 дольше grace | zombie
//     (воркеры >0, нет meaningful-трафика дольше stall).
//  2. Простой с живыми воркерами — НЕ soft (idle ≠ zombie).
//  3. Verify после soft: успех только при workers>0 И трафике ≥64KiB
//     (или MarkSoftProbeOK). Workers alone ≠ OK (иначе 18/18 @ 0 B/s).
//  4. VK через туннель — только после data-path (shouldRestoreVKThroughTunnel);
//     workers>0 лишь стартуют verify, softRecoverUntil снимается.
//  5. После softEscalateAfter неуспешных soft → full reconnect.
//  6. Короткий immune после успешного soft; storm cap для panel-flap.
const (
	softRecoverVerifyWait  = 45 * time.Second
	softRecoverTrafficNeed = int64(64 * 1024) // обязательный data-path после soft
	softStormMax           = 5                // panel-flap: больше soft до manual
	softStormWindow        = 10 * time.Minute
	softEscalateAfter      = 2 // 2 soft fail → следующий recover = full
)

// softProbeOK — явный HTTP/download probe после soft (harness / будущий in-app probe).
var softProbeOK atomic.Bool

// MarkSoftProbeOK — data-path подтверждён внешним probe (≥1 MiB download и т.п.).
func MarkSoftProbeOK() { softProbeOK.Store(true) }

func clearSoftProbeOK() { softProbeOK.Store(false) }

func softProbeMarked() bool { return softProbeOK.Load() }

// softRecoverBusy — нельзя рвать текущий soft повторным SoftReconnect
// (иначе context canceled посреди TURN/VK auth).
// Busy = только time window. softRecoverVKDirect сам по себе НЕ блокирует
// навсегда: после workers>0 finishSoft очищает until, чтобы nested soft
// на verify-fail мог стартовать (VK остаётся direct до data-path OK).
func softRecoverBusy(vkDirect bool, until, now time.Time) bool {
	_ = vkDirect
	return !until.IsZero() && now.Before(until)
}

// softAuthBeforeVerify — soft держит VK direct, воркеры ещё не поднялись
// (verify не стартовал). noteWorkerStats должен продлевать grace, а не
// запускать workers-lost soft.
func softAuthBeforeVerify(vkDirect bool, verifyUntil time.Time) bool {
	return vkDirect && verifyUntil.IsZero()
}

// shouldRestoreVKThroughTunnel — вернуть VK веб/API в туннель только когда
// есть живые воркеры И подтверждён data-path (трафик или probe).
// Workers alone ≠ OK (иначе auth dials VPN DNS 172.31.255.254 до RAWCONF).
func shouldRestoreVKThroughTunnel(workers int32, trafficOK bool, probeOK bool) bool {
	_ = workers
	_ = trafficOK
	_ = probeOK
	return false
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

// decideRecoverMode — soft пока TUN жив; после softEscalateAfter soft → full.
func decideRecoverMode(forceFull bool, softCount int, tunnelUp, wgActive bool) recoverMode {
	if forceFull {
		return recoverFull
	}
	if softCount >= softEscalateAfter {
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

// softRecoverSucceeded — soft удался только с живым data-path.
// workers>0 без трафика = ещё не успех (ждём verify window / probe).
func softRecoverSucceeded(now, lastTrafficAt time.Time, bytesSinceSoft int64, workers int32) bool {
	if softProbeMarked() {
		return true
	}
	if workers <= 0 {
		return false
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
	softTickVerifyFailFull // softEscalateAfter достигнуто → full
	softTickStallSoft
	softTickStormBlock
)

// SoftTickInput — снимок состояния для DecideSoftTick (без Orchestrator/Wails).
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
	CooldownActive bool
	StormCount     int
	StormStarted   time.Time
	SoftFailCount  int // сколько soft уже сделали в текущей серии fail
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
		// Уже softEscalateAfter soft в серии → full, не ещё soft.
		if in.SoftFailCount >= softEscalateAfter {
			out.Action = softTickVerifyFailFull
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
