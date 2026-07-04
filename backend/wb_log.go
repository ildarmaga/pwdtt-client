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

	// Per-frame media spam only — thousands/sec freeze WebView2.
	if strings.Contains(raw, "vp8tunnel:") && strings.Contains(raw, "frame #") {
		return "", "", false
	}
	if strings.Contains(raw, "[lk-video] recv vp8 frame") {
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
