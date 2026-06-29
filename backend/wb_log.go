package backend

import (
	"fmt"
	"strings"
)

// classifyWBLog maps relay/wbjrunner noise into user-facing INFO/WARN/ERROR lines
// (same idea as VK classifyLevel + formatConnectionError). Returns emit=false to drop.
func classifyWBLog(raw string) (level, msg string, emit bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	low := strings.ToLower(raw)

	// VP8 / media tunnel — never spam the UI (was thousands/sec and froze WebView).
	if strings.Contains(raw, "vp8tunnel:") && strings.Contains(raw, "frame #") {
		return "", "", false
	}
	if strings.Contains(raw, "[lk-video] recv vp8 frame") {
		return "", "", false
	}
	if strings.Contains(raw, "[lk] <- signal kind=") {
		return "", "", false
	}
	if strings.Contains(raw, "[lk] ping #") {
		return "", "", false
	}
	if strings.Contains(raw, "[lk] pub local cand:") || strings.Contains(raw, "[lk] sub local cand:") {
		return "", "", false
	}
	if strings.Contains(raw, "[lk] <- trickle") || strings.Contains(raw, "[lk] <- sub offer") ||
		strings.Contains(raw, "[lk] <- pub answer") {
		return "", "", false
	}
	if strings.Contains(raw, "[lk] pub ICE gathering") || strings.Contains(raw, "[lk] sub ICE gathering") {
		return "", "", false
	}
	if strings.HasPrefix(raw, "[bypass]") && strings.Contains(raw, "pre-tun=") {
		return "", "", false
	}
	if strings.HasPrefix(raw, "[desktoptun] LAN bypass") {
		return "", "", false
	}

	// Errors
	if strings.Contains(raw, "guests cannot create rooms") {
		return "ERROR", "[WB] Сервер не вещает в комнату — owner/creator offline, обратитесь к админу", true
	}
	if strings.Contains(low, "fatal") || strings.Contains(low, " error") ||
		strings.Contains(low, "ошибка") || strings.Contains(low, "failed") && !strings.Contains(raw, "retry") {
		return "ERROR", "[WB] " + raw, true
	}

	// Warnings / retries
	if strings.Contains(low, "warn") || strings.Contains(raw, "[wbt] session:") ||
		strings.Contains(low, "retry") || strings.Contains(low, "недоступен") {
		return "WARN", "[WB] " + raw, true
	}

	// Friendly one-liners (Russian, like VK [WG] / [СОСТОЯНИЕ])
	switch {
	case strings.Contains(raw, "TUNNEL CONNECTED"):
		return "INFO", "[WB] Туннель WebRTC подключён", true
	case strings.Contains(raw, "[warmup] joiner ready"):
		if ip := extractAfter(raw, "ip="); ip != "" {
			return "INFO", fmt.Sprintf("[WB] Joiner проверен · IP %s", ip), true
		}
		return "INFO", "[WB] Joiner проверен", true
	case strings.Contains(raw, "[warmup] traffic ready"):
		if ip := extractAfter(raw, "ip="); ip != "" {
			return "INFO", fmt.Sprintf("[WB] Трафик через туннель проверен · IP %s", ip), true
		}
		return "INFO", "[WB] Трафик через туннель проверен", true
	case strings.Contains(raw, "[warmup] OS route"):
		if ip := extractAfter(raw, "ip="); ip != "" {
			return "INFO", fmt.Sprintf("[WB] Браузерный маршрут · IP %s", ip), true
		}
		return "INFO", "[WB] Браузерный маршрут проверен", true
	case strings.Contains(raw, "TUN ACTIVE"):
		return "INFO", "[WB] VPN-адаптер поднят (WDTT-WB)", true
	case strings.Contains(raw, "[wbt] room="):
		return "INFO", "[WB] " + raw, true
	case strings.Contains(raw, "[lk] pub ICE state: connected") || strings.Contains(raw, "[lk] sub ICE state: connected"):
		return "INFO", "[WB] WebRTC ICE connected", true
	case strings.Contains(raw, "[lk] pub PC state: connected") || strings.Contains(raw, "[lk] sub PC state: connected"):
		return "", "", false // duplicate with ICE connected
	case strings.Contains(raw, "[lk] vp8 tunnel writer started"):
		return "INFO", "[WB] Канал данных VP8 активен", true
	case strings.Contains(raw, "[lk] dc tunnel ready"):
		return "INFO", "[WB] DataChannel готов", true
	case strings.Contains(raw, "[lk] reliable DC open") || strings.Contains(raw, "[lk] remote _reliable DC open"):
		return "", "", false
	case strings.Contains(raw, "[lk] sub remote track:"):
		return "INFO", "[WB] Видеотрек от сервера получен", true
	case strings.Contains(raw, "[desktoptun] up:"):
		return "INFO", "[WB] Маршруты VPN установлены", true
	case strings.Contains(raw, "[desktoptun] direct netstack up"):
		return "INFO", "[WB] Netstack активен (без SOCKS)", true
	case strings.Contains(raw, "[desktoptun] original default gateway"):
		return "INFO", "[WB] Шлюз LAN: " + extractGateway(raw), true
	case strings.Contains(raw, "[wbt] tunnel swapped"):
		return "INFO", "[WB] Туннель переподключён", true
	case strings.Contains(raw, "joiner WBT: awaiting inbound"):
		return "INFO", "[WB] Ожидание медиаканала от сервера…", true
	case strings.Contains(raw, "joiner WBT: sub ICE ready"):
		return "INFO", "[WB] Sub ICE готов · поднимаю KCP", true
	case strings.Contains(raw, "signaling bypass refreshed"):
		return "INFO", "[WB] Переподключение: обновляю bypass для stream.wb.ru…", true
	case strings.Contains(raw, "WebRTC session ended") || strings.Contains(raw, "joiner cleared"):
		return "WARN", "[WB] Сессия WebRTC завершена · переподключение…", true
	case strings.Contains(raw, "tunnel rebound"):
		return "INFO", "[WB] Туннель переподключён (carrier swap)", true
	case strings.Contains(raw, "tunnel already active"):
		return "", "", false
	case strings.Contains(raw, "[wb] sub ICE connected — rebinding"):
		return "", "", false
	case strings.Contains(raw, "[wbt] obf localEpoch"):
		return "", "", false
	case strings.Contains(raw, "vp8tunnel: writer (re)started"):
		return "", "", false
	case strings.HasPrefix(raw, "[desktoptun] bypass "):
		return "", "", false
	}

	// ICE disconnect — warn once
	if strings.Contains(raw, "ICE state: disconnected") || strings.Contains(raw, "PC state: disconnected") {
		return "WARN", "[WB] WebRTC: разрыв, переподключение…", true
	}

	// Default: short technical lines as INFO only if they look important
	if strings.HasPrefix(raw, "[lk]") || strings.HasPrefix(raw, "vp8tunnel:") {
		return "", "", false
	}
	if strings.HasPrefix(raw, "[desktoptun]") {
		return "", "", false
	}

	return "INFO", "[WB] " + raw, true
}

func extractAfter(s, prefix string) string {
	i := strings.Index(s, prefix)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(s[i+len(prefix):])
	if sp := strings.IndexAny(rest, " )"); sp > 0 {
		return rest[:sp]
	}
	return rest
}

func extractGateway(line string) string {
	if i := strings.Index(line, "gateway "); i >= 0 {
		rest := line[i+len("gateway "):]
		if j := strings.Index(rest, " via "); j > 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	return ""
}
