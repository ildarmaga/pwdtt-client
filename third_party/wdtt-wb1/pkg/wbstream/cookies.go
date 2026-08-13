package wbstream

import (
	"encoding/json"
	"fmt"
	"strings"
)

const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"

// CookieAllowlist — cookies для slide-v3 refresh WB Stream.
var CookieAllowlist = []string{
	"wbx-refresh",
	"x_wbaas_token",
	"_wbauid",
	"wbx-validation-key",
}

type cookieEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CookiesJSONToHeader парсит JSON-массив cookies в заголовок Cookie.
func CookiesJSONToHeader(raw []byte) (string, error) {
	var cookies []cookieEntry
	if err := json.Unmarshal(raw, &cookies); err != nil {
		return "", fmt.Errorf("неверный JSON cookies: %w", err)
	}
	if len(cookies) == 0 {
		return "", fmt.Errorf("файл cookies пуст")
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if strings.TrimSpace(c.Name) == "" {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("cookies пусты")
	}
	return strings.Join(parts, "; "), nil
}

// DeviceIDFromHeader извлекает __wb_device_id из Cookie-заголовка.
func DeviceIDFromHeader(cookieHeader string) string {
	return CookieValue(cookieHeader, "__wb_device_id")
}

func CookieValue(cookieHeader, name string) string {
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		eq := strings.IndexByte(part, '=')
		if eq != -1 && part[:eq] == name {
			return part[eq+1:]
		}
	}
	return ""
}

// FilterCookies оставляет только allowlist (для slide-v3).
func FilterCookies(cookieHeader string, allow []string) string {
	allowed := make(map[string]struct{}, len(allow))
	for _, n := range allow {
		allowed[n] = struct{}{}
	}
	var out []string
	for _, part := range strings.Split(cookieHeader, ";") {
		trimmed := strings.TrimSpace(part)
		eq := strings.IndexByte(trimmed, '=')
		if eq == -1 {
			continue
		}
		if _, ok := allowed[trimmed[:eq]]; ok {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "; ")
}

// ValidateCookiesJSON проверяет наличие __wb_device_id.
func ValidateCookiesJSON(raw []byte) error {
	header, err := CookiesJSONToHeader(raw)
	if err != nil {
		return err
	}
	if DeviceIDFromHeader(header) == "" {
		return fmt.Errorf("в cookies нет __wb_device_id — экспортируйте через creator-app (Export Cookies)")
	}
	filtered := FilterCookies(header, CookieAllowlist)
	if filtered == "" {
		return fmt.Errorf("нет cookies для WB auth (%s)", strings.Join(CookieAllowlist, ", "))
	}
	return nil
}
