//go:build linux

package backend

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/tun"
)

var (
	activeRoutes   []string
	activeRoutesMu sync.Mutex
	activeRawTun   tun.Device
)

func wgTunnelActive() bool {
	return exec.Command("ip", "link", "show", wgIface).Run() == nil
}

func applyWGConfig(conf string, turnIPs []string) error {
	addr, mtu, allowedIPs, wgConf := parseWGConfig(conf)
	if addr == "" {
		return fmt.Errorf("Address not found in wg config")
	}

	// Проверяем доступность sudo без пароля
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		return fmt.Errorf("sudo недоступен (нет прав или требует пароль): %w", err)
	}

	tmp, err := os.CreateTemp("", "wg-turn-*.conf")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(wgConf); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	// Делаем файл читаемым для root (sudo)
	_ = os.Chmod(tmpName, 0644)

	if SoftReconnectPreserve() && wgTunnelActive() && activeRawTun == nil {
		// Не сносим link/routes — только sync ключей peer (иначе zombie после рестарта сервера).
		if err := run("wg", "setconf", wgIface, tmpName); err != nil {
			return fmt.Errorf("soft wg setconf: %w", err)
		}
		EnsureTurnDirectRoutes(turnIPs)
		markSoftPreserveConsumed()
		log.Printf("[WG] Soft-reconnect: интерфейс %s сохранён, ключи WG обновлены", wgIface)
		return nil
	}
	if SoftReconnectPreserve() {
		log.Printf("[WG] Soft: нет WG device — полный create")
		markSoftPreserveConsumed()
	}
	teardownWG()

	if err := run("ip", "link", "add", wgIface, "type", "wireguard"); err != nil {
		return fmt.Errorf("ip link add: %w", err)
	}

	if err := run("wg", "setconf", wgIface, tmpName); err != nil {
		return fmt.Errorf("wg setconf: %w", err)
	}

	_ = run("ip", "addr", "flush", "dev", wgIface)
	if err := run("ip", "addr", "add", addr, "dev", wgIface); err != nil {
		return fmt.Errorf("ip addr add: %w", err)
	}
	if mtu != "" {
		_ = run("ip", "link", "set", wgIface, "mtu", mtu)
	}
	if err := run("ip", "link", "set", wgIface, "up"); err != nil {
		return fmt.Errorf("ip link set up: %w", err)
	}

	var routes []string
	gw := defaultGateway()
	rememberWGGateway(gw)
	if gw != "" {
		for _, ip := range turnIPs {
			cidr := ip + "/32"
			if run("ip", "route", "add", cidr, "via", gw) == nil {
				routes = append(routes, cidr)
			}
		}
		if err := installVKExcludeRoutes(gw); err == nil {
			markVKExcludeInstalled()
		}
		for _, dns := range localDNSServers() {
			cidr := dns + "/32"
			if run("ip", "route", "add", cidr, "via", gw) == nil {
				routes = append(routes, cidr)
			}
		}
	}
	for _, cidr := range allowedIPs {
		if run("ip", "route", "add", cidr, "dev", wgIface) == nil {
			routes = append(routes, "dev:"+cidr)
		}
	}

	activeRoutesMu.Lock()
	activeRoutes = routes
	activeRoutesMu.Unlock()
	return nil
}

// rawSoftTun — TUN для RAW soft-rebind.
func rawSoftTun() tun.Device {
	return activeRawTun
}

func applyRawConfig(conf string, turnIPs []string) error {
	if SoftReconnectPreserve() && rawSoftTun() != nil && wgTunnelActive() {
		EnsureTurnDirectRoutes(turnIPs)
		if err := rebindRawBridgeSoft("9000"); err != nil {
			return fmt.Errorf("soft raw bridge: %w", err)
		}
		markSoftPreserveConsumed()
		log.Printf("[RAW] Soft-reconnect: интерфейс %s сохранён, bridge + TURN routes обновлены", wgIface)
		return nil
	}
	if SoftReconnectPreserve() {
		log.Printf("[RAW] Soft: нет RAW TUN — полный create")
		markSoftPreserveConsumed()
	}
	teardownWG()

	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		return fmt.Errorf("sudo недоступен (нет прав или требует пароль): %w", err)
	}

	ip, _, mtu, err := parseRawConfig(conf)
	if err != nil {
		return err
	}

	tunDev, err := tun.CreateTUN(wgIface, mtu)
	if err != nil {
		return fmt.Errorf("create TUN: %w", err)
	}
	tunDev = wrapTrafficTun(tunDev)
	activeRawTun = tunDev
	setActiveRawPrimaryIP(ip)

	_ = run("ip", "addr", "flush", "dev", wgIface)
	if err := run("ip", "addr", "add", ip+"/16", "dev", wgIface); err != nil {
		teardownWG()
		return fmt.Errorf("ip addr add: %w", err)
	}
	_ = run("ip", "link", "set", wgIface, "mtu", fmt.Sprintf("%d", mtu))
	if err := run("ip", "link", "set", wgIface, "up"); err != nil {
		teardownWG()
		return fmt.Errorf("ip link set up: %w", err)
	}

	var routes []string
	gw := defaultGateway()
	rememberWGGateway(gw)
	if gw != "" {
		// Transport excludes до split-default (91.231 и др.).
		for _, tip := range turnIPs {
			if net.ParseIP(tip) == nil {
				continue
			}
			cidr := tip + "/32"
			if run("ip", "route", "add", cidr, "via", gw) == nil {
				routes = append(routes, cidr)
			}
		}
		if err := installVKTransportRoutes(gw); err == nil {
			markTransportDirectInstalled()
		}
		for _, dns := range localDNSServers() {
			cidr := dns + "/32"
			if run("ip", "route", "add", cidr, "via", gw) == nil {
				routes = append(routes, cidr)
			}
		}
	}
	for _, cidr := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if run("ip", "route", "add", cidr, "dev", wgIface) == nil {
			routes = append(routes, "dev:"+cidr)
		}
	}

	activeRoutesMu.Lock()
	activeRoutes = routes
	activeRoutesMu.Unlock()

	if err := startRawBridge(tunDev, "9000"); err != nil {
		teardownWG()
		return fmt.Errorf("raw bridge: %w", err)
	}

	log.Printf("[RAW] Туннель %s поднят (ip=%s mtu=%d, без WireGuard)", wgIface, ip, mtu)
	return nil
}

// EnsureTurnDirectRoutes — live update TURN /32 + transport CIDR.
func EnsureTurnDirectRoutes(turnIPs []string) {
	if !wgTunnelActive() && activeRawTun == nil {
		return
	}
	gw := defaultGateway()
	if gw == "" {
		vkRouteMu.Lock()
		gw = wgGateway
		vkRouteMu.Unlock()
	}
	if gw == "" {
		return
	}
	rememberWGGateway(gw)
	activeRoutesMu.Lock()
	defer activeRoutesMu.Unlock()
	for _, tip := range turnIPs {
		if net.ParseIP(tip) == nil {
			continue
		}
		cidr := tip + "/32"
		if run("ip", "route", "add", cidr, "via", gw) == nil {
			activeRoutes = append(activeRoutes, cidr)
		}
	}
	_ = installVKTransportRoutes(gw)
}

func teardownWG() {
	stopRawBridge()

	activeRoutesMu.Lock()
	routes := activeRoutes
	activeRoutes = nil
	activeRoutesMu.Unlock()

	for _, entry := range routes {
		if strings.HasPrefix(entry, "dev:") {
			cidr := strings.TrimPrefix(entry, "dev:")
			_ = run("ip", "route", "del", cidr, "dev", wgIface)
		} else {
			_ = run("ip", "route", "del", entry)
		}
	}
	if activeRawTun != nil {
		_ = activeRawTun.Close()
		activeRawTun = nil
	} else {
		_ = run("ip", "link", "del", wgIface)
	}
	clearActiveRawPrimaryIP()
	clearWGRouteState()
}

func run(name string, args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n", name}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w — %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func defaultGateway() string {
	cmd := exec.Command("ip", "route", "show", "default")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

func localDNSServers() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(fields[1])
		// Пропускаем loopback (127.x.x.x, ::1) — маршрут на него бессмысленен
		if ip == nil || ip.IsLoopback() {
			continue
		}
		result = append(result, fields[1])
	}
	return result
}
