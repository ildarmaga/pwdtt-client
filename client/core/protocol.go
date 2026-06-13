package core

import (
	"fmt"
	"net"
	"strings"
	"time"
)

var deniedMessages = map[string]string{
	"wrong_password":    "FATAL_AUTH: неверный пароль подключения",
	"expired":           "FATAL_AUTH: срок действия пароля истёк",
	"device_mismatch":   "FATAL_AUTH: пароль привязан к другому устройству",
	"deactivated":       "FATAL_AUTH: пароль деактивирован администратором",
	"too_many_sessions": "FATAL_AUTH: слишком много параллельных подключений с этого устройства",
	"traffic_exceeded":  "FATAL_AUTH: лимит трафика исчерпан",
}

// RequestConfig запрашивает WireGuard конфиг через DTLS-соединение.
func RequestConfig(conn net.Conn, localPort, deviceID, password string) (string, error) {
	payload := fmt.Sprintf("GETCONF:%s|%s|%s", localPort, deviceID, password)
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", fmt.Errorf("отправка GETCONF: %w", err)
	}

	b := make([]byte, 4096)
	if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return "", fmt.Errorf("установка дедлайна: %w", err)
	}
	n, err := conn.Read(b)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return "", fmt.Errorf("чтение ответа конфига: %w", err)
	}

	resp := string(b[:n])
	if resp == "NOCONF" {
		return "", nil
	}

	if strings.HasPrefix(resp, "DENIED:") {
		reason := strings.TrimPrefix(resp, "DENIED:")
		if msg, ok := deniedMessages[reason]; ok {
			return "", fmt.Errorf("%s", msg)
		}
		return "", fmt.Errorf("FATAL_AUTH: доступ запрещён (%s)", reason)
	}

	return resp, nil
}


