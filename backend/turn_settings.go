package backend

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	core "wg-turn-client/core"
)

type TURNSettings struct {
	Preferred []string `json:"preferred"`
	Relays    []string `json:"relays"`
}
type TURNProbe struct {
	Address   string  `json:"address"`
	Replies   int     `json:"replies"`
	Probes    int     `json:"probes"`
	AverageMs float64 `json:"averageMs"`
	MinMs     float64 `json:"minMs"`
	MaxMs     float64 `json:"maxMs"`
}

var turnSettingsMu sync.Mutex
var turnDiscoveryMu sync.Mutex

func (a *App) GetActiveTURNWorkers() map[string]int {
	return core.ActiveTURNWorkers()
}

// Discover credentials without creating TURN allocations, TUN, or VPN workers.
func (a *App) DiscoverTURNRelays(hashes []string) (TURNSettings, error) {
	if a.orch != nil && a.orch.IsRunning() && len(core.KnownTURNURLs()) > 0 {
		return a.GetTURNSettings(), nil
	}
	if !turnDiscoveryMu.TryLock() {
		return TURNSettings{}, fmt.Errorf("проверка уже выполняется")
	}
	defer turnDiscoveryMu.Unlock()
	if len(hashes) > 4 {
		hashes = hashes[:4]
	}
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	count := 0
	var lastErr error
	seen := map[string]bool{}
	for i, hash := range hashes {
		hash = strings.TrimSpace(hash)
		if hash == "" || seen[hash] {
			continue
		}
		seen[hash] = true
		captcha := make(chan string, 1)
		_, _, urls, err := core.GetCreds(ctx, hash, (i+1)*100, captcha, func() string { return "wv" }, func(_, _, _ string) {
			cancel()
			select {
			case captcha <- "":
			default:
			}
		})
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		core.RememberTURNURLs(urls)
		count += len(urls)
	}
	if count == 0 {
		if lastErr != nil {
			return TURNSettings{}, fmt.Errorf("не удалось получить TURN: %w", lastErr)
		}
		return TURNSettings{}, fmt.Errorf("в выбранном профиле нет VK-хешей")
	}
	return a.GetTURNSettings(), nil
}

func readTURNSettings() TURNSettings {
	s := TURNSettings{Preferred: []string{}, Relays: []string{}}
	if data, err := os.ReadFile(filepath.Join(configDir(), "turn-selection.json")); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func (a *App) GetTURNSettings() TURNSettings {
	turnSettingsMu.Lock()
	defer turnSettingsMu.Unlock()
	s := readTURNSettings()
	known := map[string]bool{}
	for _, url := range append(s.Relays, core.KnownTURNURLs()...) {
		host, port, err := net.SplitHostPort(url)
		if err == nil && port == "19302" && net.ParseIP(host).To4() != nil {
			known[url] = true
		}
	}
	s.Relays = []string{}
	for url := range known {
		s.Relays = append(s.Relays, url)
	}
	sort.Strings(s.Relays)
	// Retain discovery for the settings screen before the next connection.
	if len(s.Relays) > 0 {
		_ = writeTURNSettings(s)
	}
	return s
}

func writeTURNSettings(s TURNSettings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(configDir(), "turn-selection.json")
	if err := os.MkdirAll(configDir(), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return nil
}

func (a *App) SaveTURNSelection(preferred []string) error {
	if len(preferred) > 32 {
		return fmt.Errorf("не более 32 адресов или подсетей")
	}
	cleaned := []string{}
	for _, item := range preferred {
		item = strings.TrimSpace(item)
		ip := net.ParseIP(item)
		_, subnet, err := net.ParseCIDR(item)
		if (ip == nil || ip.To4() == nil) && (err != nil || subnet.IP.To4() == nil) {
			return fmt.Errorf("неверный IPv4 адрес или подсеть: %s", item)
		}
		cleaned = append(cleaned, item)
	}
	turnSettingsMu.Lock()
	defer turnSettingsMu.Unlock()
	s := readTURNSettings()
	s.Preferred = cleaned
	if err := writeTURNSettings(s); err != nil {
		return err
	}
	core.SetRelayPreferences(cleaned)
	return nil
}

func (a *App) ProbeTURNRelays() []TURNProbe {
	settings := a.GetTURNSettings()
	ips := []string{}
	for _, url := range settings.Relays {
		host, _, _ := net.SplitHostPort(url)
		ips = append(ips, host)
	}
	ifaceMu.Lock()
	EnsureTurnDirectRoutes(ips)
	ifaceMu.Unlock()
	results := make([]TURNProbe, len(settings.Relays))
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for i, url := range settings.Relays {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			result := TURNProbe{Address: url, Probes: 6}
			for attempt := 0; attempt < 6; attempt++ {
				conn, err := net.DialTimeout("udp4", url, time.Second)
				if err != nil {
					continue
				}
				packet := make([]byte, 20)
				packet[1] = 1
				copy(packet[4:8], []byte{0x21, 0x12, 0xa4, 0x42})
				if _, err := rand.Read(packet[8:]); err != nil {
					conn.Close()
					continue
				}
				_ = conn.SetDeadline(time.Now().Add(900 * time.Millisecond))
				started := time.Now()
				_, err = conn.Write(packet)
				buf := make([]byte, 512)
				n := 0
				if err == nil {
					n, err = conn.Read(buf)
				}
				elapsed := float64(time.Since(started).Microseconds()) / 1000
				conn.Close()
				if err != nil || n < 20 || buf[0] != 1 || buf[1] != 1 || string(buf[4:20]) != string(packet[4:20]) {
					continue
				}
				result.Replies++
				result.AverageMs += elapsed
				if result.MinMs == 0 || elapsed < result.MinMs {
					result.MinMs = elapsed
				}
				if elapsed > result.MaxMs {
					result.MaxMs = elapsed
				}
			}
			if result.Replies > 0 {
				result.AverageMs /= float64(result.Replies)
			}
			results[i] = result
		}(i, url)
	}
	wg.Wait()
	return results
}
