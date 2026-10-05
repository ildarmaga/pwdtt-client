package core

import (
	"encoding/hex"
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

// csqttGetconfPayload — 5 полей: порт, устройство, пароль, воркер, Audio|Video.
func csqttGetconfPayload(localPort, deviceID, password, obfs string) string {
	extra := "Audio"
	if obfs == "video" {
		extra = "Video"
	}
	return fmt.Sprintf("GETCONF:%s|%s|%s|0|%s", localPort, deviceID, password, extra)
}

// RequestCSQTTConfig шлёт GETCONF внутри WRAP (без DTLS) и переводит TUNCONF в RAW-конфиг.
func RequestCSQTTConfig(conn net.Conn, localPort, deviceID, password, obfs string, mtu int) (string, error) {
	resp, err := requestConfigPayload(conn, csqttGetconfPayload(localPort, deviceID, password, obfs))
	if err != nil || resp == "" {
		return resp, err
	}
	ip, dns, ok := parseTUNCONF(resp)
	if !ok {
		return "", fmt.Errorf("неожиданный ответ CSQTT (нет TUNCONF): %q", trimProtoPreview(resp, 64))
	}
	return formatCSQTTRawConfig(ip, dns, mtu), nil
}

// parseTUNCONF разбирает TUNCONF:ip:dns:port. У CSQTT нет DTLS и нет WireGuard-конфига.
func parseTUNCONF(resp string) (ip, dns string, ok bool) {
	const prefix = "TUNCONF:"
	s := strings.TrimSpace(resp)
	if !strings.HasPrefix(s, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(s, prefix), ":")
	if len(parts) < 2 {
		return "", "", false
	}
	ip = strings.TrimSpace(parts[0])
	dns = strings.TrimSpace(parts[1])
	if net.ParseIP(ip) == nil || net.ParseIP(dns) == nil {
		return "", "", false
	}
	return ip, dns, true
}

func formatCSQTTRawConfig(ip, dns string, mtu int) string {
	if mtu < 576 {
		mtu = 1280
	}
	return fmt.Sprintf("IP = %s\nDNS = %s\nMTU = %d\n", ip, dns, mtu)
}

// RequestConfig запрашивает WireGuard конфиг через DTLS-соединение.
func RequestConfig(conn net.Conn, localPort, deviceID, password string) (string, error) {
	payload := fmt.Sprintf("GETCONF:%s|%s|%s", localPort, deviceID, password)
	return requestConfigPayload(conn, payload)
}

func requestConfigPayload(conn net.Conn, payload string) (string, error) {
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

// RequestRawConfig запрашивает IP для RAW-режима (без WireGuard): RAWCONF:device|pass|mtu.
func RequestRawConfig(conn net.Conn, deviceID, password string, mtu int) (string, error) {
	return RequestRawConfigCapabilities(conn, deviceID, password, mtu, false)
}

func RequestRawConfigCapabilities(conn net.Conn, deviceID, password string, mtu int, requireChunk bool) (string, error) {
	if mtu < 576 {
		mtu = 1280
	}
	payload := fmt.Sprintf("RAWCONF:%s|%s|%d|CHAL1", deviceID, password, mtu)
	if requireChunk {
		payload += "|CHUNK1"
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", fmt.Errorf("отправка RAWCONF: %w", err)
	}

	readResponse := func() (string, error) {
		b := make([]byte, 4096)
		if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return "", fmt.Errorf("установка дедлайна: %w", err)
		}
		n, err := conn.Read(b)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			return "", fmt.Errorf("чтение ответа RAWCONF: %w", err)
		}
		return string(b[:n]), nil
	}
	resp, err := readResponse()
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(resp, "RAWCHAL:") {
		challenge := strings.TrimSpace(strings.TrimPrefix(resp, "RAWCHAL:"))
		decoded, decodeErr := hex.DecodeString(challenge)
		if decodeErr != nil || len(decoded) != 16 {
			return "", fmt.Errorf("некорректный RAW challenge")
		}
		if _, err := conn.Write([]byte(payload + "|CHAL=" + challenge)); err != nil {
			return "", fmt.Errorf("ответ RAW challenge: %w", err)
		}
		resp, err = readResponse()
		if err != nil {
			return "", err
		}
	}
	if resp == "NOCONF" {
		return "", fmt.Errorf("сервер не выдал RAW-конфиг (нужен wdtt с поддержкой RAW)")
	}
	if strings.HasPrefix(resp, "DENIED:") {
		reason := strings.TrimPrefix(resp, "DENIED:")
		if msg, ok := deniedMessages[reason]; ok {
			return "", fmt.Errorf("%s", msg)
		}
		return "", fmt.Errorf("FATAL_AUTH: доступ запрещён (%s)", reason)
	}
	if !strings.Contains(resp, "IP =") && !strings.Contains(resp, "IP=") {
		return "", fmt.Errorf("неожиданный ответ RAWCONF (сервер без RAW?): %q", trimProtoPreview(resp, 64))
	}
	if requireChunk && !strings.Contains(resp, "CAPS = CHUNK1") {
		return "", fmt.Errorf("FATAL_RAW_CAP: сервер не поддерживает CHUNK1 (нужен server >= 1.4.134)")
	}
	return resp, nil
}

func trimProtoPreview(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
