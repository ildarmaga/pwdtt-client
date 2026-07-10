package backend

import (
	"strings"
)

// classifyWBLog maps relay/wbjrunner lines to UI log entries.
// Diagnostic mode: pass almost everything; only drop per-frame VP8 spam (freezes WebView).
func classifyWBLog(raw string) (level, msg string, emit bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	low := strings.ToLower(raw)

	// Benign SOCKS teardown (app/CDN closed socket) — already filtered in
	// relay IsBenignConnError, but keep a UI safety net for older relay builds
	// and Windows wsarecv wording that used to flood [ERROR].
	if strings.Contains(raw, "relay: SOCKS") && strings.Contains(raw, "read error:") {
		if strings.Contains(low, "use of closed") ||
			strings.Contains(low, "forcibly closed") ||
			strings.Contains(low, "wsarecv") ||
			strings.Contains(low, "connection reset") {
			return "", "", false
		}
	}

	// Per-frame media spam only — thousands/sec freeze WebView2.
	if strings.Contains(raw, "vp8tunnel:") && strings.Contains(raw, "frame #") {
		return "", "", false
	}
	if strings.Contains(raw, "[lk-video] recv vp8 frame") {
		return "", "", false
	}
	// xray access log — filtered in wbxray/runner.go before relay; drop here too if leaked.
	if strings.Contains(raw, "[xray]") && strings.Contains(raw, " accepted ") {
		return "", "", false
	}

	if strings.Contains(raw, "guests cannot create rooms") {
		return "ERROR", "[WB] Сервер не вещает в комнату — owner/creator offline, обратитесь к админу", true
	}

	level = "INFO"
	switch {
	case strings.Contains(low, "fatal"):
		level = "ERROR"
	case strings.Contains(low, " error") || strings.Contains(low, "ошибка"):
		level = "ERROR"
	case strings.Contains(low, "failed") && !strings.Contains(raw, "retry"):
		level = "ERROR"
	case strings.Contains(low, "warn") || strings.Contains(raw, "[wbt] session:") ||
		strings.Contains(low, "retry") || strings.Contains(low, "недоступен"):
		level = "WARN"
	case strings.Contains(raw, "ICE state: disconnected") || strings.Contains(raw, "PC state: disconnected"):
		level = "WARN"
	}

	return level, "[WB] " + raw, true
}
