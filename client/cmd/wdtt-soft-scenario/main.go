// wdtt-soft-scenario — live soft/hard recovery matrix (RAW|WG).
// Успех = download ≥ min bytes после recover (не «workers>0»).
//
//	WDTT_PASS=... WDTT_HASHES=h1,h2 WDTT_PEER=host:56000 \
//	  ./wdtt-soft-scenario -mode raw -scenario connect_download
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.zx2c4.com/wireguard/tun"
	"wg-turn-client/core"
)

const (
	rawIface   = "wdtt-sraw"
	rawNS      = "wdttsoft"
	wgIface    = "wdtt-swg"
	wgNS       = "wdttsoftwg"
	vethHost   = "veth-ssh"
	vethNS     = "veth-ssn"
	vethHostIP = "10.255.253.1"
	vethNSIP   = "10.255.253.2"
	rawListen  = "19010"
	wgListen   = "19011"
)

func main() {
	mode := flag.String("mode", "raw", "raw|wg")
	scenario := flag.String("scenario", "connect_download",
		"connect_download|soft_core_die|hard_reconnect|idle_no_soft|panel_restart|workers_zero")
	transport := flag.String("transport", "tcp", "tcp|udp")
	workers := flag.Int("workers", 9, "workers")
	gw := flag.String("gw", "10.66.66.1", "tunnel gateway / blob host")
	minMB := flag.Float64("min-mb", 1, "min downloaded MiB to pass")
	blobPort := flag.Int("blob-port", 18080, "blob HTTP port on gateway")
	panelRestartWait := flag.Int("panel-wait", 90, "seconds to wait for recover after panel restart signal")
	flag.Parse()

	if *mode != "raw" && *mode != "wg" {
		log.Fatal("-mode raw|wg")
	}
	pass := env("WDTT_PASS", "")
	peer := env("WDTT_PEER", "")
	hashes := splitCSV(env("WDTT_HASHES", ""))
	device := env("WDTT_DEVICE", "soft-"+*mode+"-"+strconv.FormatInt(time.Now().Unix()%100000, 10))
	cfgRoot := env("WDTT_CFG", "/tmp/pwdtt-soft-cfg-"+*mode)
	if pass == "" || peer == "" || len(hashes) == 0 {
		log.Fatal("need WDTT_PASS WDTT_PEER WDTT_HASHES")
	}
	core.SetConfigRoot(cfgRoot)

	blobURL := fmt.Sprintf("http://%s:%d/blob?bytes=%d", *gw, *blobPort, 2*1024*1024)
	minBytes := int64(*minMB * 1024 * 1024)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("SCENARIO=%s mode=%s peer=%s gw=%s", *scenario, *mode, peer, *gw)
	var err error
	switch *scenario {
	case "connect_download":
		err = runConnectDownload(ctx, *mode, *transport, peer, pass, hashes, device, *workers, *gw, blobURL, minBytes)
	case "soft_core_die":
		err = runSoftCoreDie(ctx, *mode, *transport, peer, pass, hashes, device, *workers, *gw, blobURL, minBytes)
	case "hard_reconnect":
		err = runHardReconnect(ctx, *mode, *transport, peer, pass, hashes, device, *workers, *gw, blobURL, minBytes)
	case "idle_no_soft":
		err = runIdleNoSoft(ctx, *mode, *transport, peer, pass, hashes, device, *workers, *gw, blobURL, minBytes)
	case "panel_restart":
		err = runPanelRestart(ctx, *mode, *transport, peer, pass, hashes, device, *workers, *gw, blobURL, minBytes, *panelRestartWait)
	case "workers_zero":
		err = runWorkersZero(ctx, *mode, *transport, peer, pass, hashes, device, *workers, *gw, blobURL, minBytes)
	default:
		log.Fatalf("unknown scenario %q", *scenario)
	}
	if err != nil {
		fmt.Printf("SCENARIO_FAIL mode=%s scenario=%s err=%v\n", *mode, *scenario, err)
		os.Exit(1)
	}
	fmt.Printf("SCENARIO_PASS mode=%s scenario=%s\n", *mode, *scenario)
}

// --- scenarios ---

func runConnectDownload(ctx context.Context, mode, transport, peer, pass string, hashes []string, device string, workers int, gw, blobURL string, minBytes int64) error {
	s, err := startSession(ctx, mode, transport, peer, pass, hashes, device, workers, gw, false)
	if err != nil {
		return err
	}
	defer s.close()
	return s.download(ctx, blobURL, minBytes, 30*time.Second)
}

func runSoftCoreDie(ctx context.Context, mode, transport, peer, pass string, hashes []string, device string, workers int, gw, blobURL string, minBytes int64) error {
	s, err := startSession(ctx, mode, transport, peer, pass, hashes, device, workers, gw, false)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.download(ctx, blobURL, minBytes, 30*time.Second); err != nil {
		return fmt.Errorf("pre-soft download: %w", err)
	}
	t0 := time.Now()
	if err := s.softRestartCore(ctx, transport, peer, pass, hashes, device, workers); err != nil {
		return fmt.Errorf("soft restart: %w", err)
	}
	if err := s.download(ctx, blobURL, minBytes, 45*time.Second); err != nil {
		return fmt.Errorf("post-soft download: %w", err)
	}
	fmt.Printf("SOFT_RECOVER_SEC=%.1f\n", time.Since(t0).Seconds())
	return nil
}

func runHardReconnect(ctx context.Context, mode, transport, peer, pass string, hashes []string, device string, workers int, gw, blobURL string, minBytes int64) error {
	s, err := startSession(ctx, mode, transport, peer, pass, hashes, device, workers, gw, false)
	if err != nil {
		return err
	}
	_ = s.download(ctx, blobURL, minBytes, 30*time.Second)
	s.close()
	time.Sleep(3 * time.Second)
	// Same device_id — password stays bound to this device.
	s2, err := startSession(ctx, mode, transport, peer, pass, hashes, device, workers, gw, false)
	if err != nil {
		return fmt.Errorf("hard reconnect start: %w", err)
	}
	defer s2.close()
	return s2.download(ctx, blobURL, minBytes, 45*time.Second)
}

func runIdleNoSoft(ctx context.Context, mode, transport, peer, pass string, hashes []string, device string, workers int, gw, blobURL string, minBytes int64) error {
	s, err := startSession(ctx, mode, transport, peer, pass, hashes, device, workers, gw, false)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.download(ctx, blobURL, minBytes, 30*time.Second); err != nil {
		return err
	}
	softBefore := s.softRestarts.Load()
	log.Printf("idle 120s (expect 0 soft restarts)…")
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
		if s.softRestarts.Load() != softBefore {
			return fmt.Errorf("unexpected soft during idle")
		}
	}
	fmt.Printf("IDLE_SOFT_COUNT=%d\n", s.softRestarts.Load()-softBefore)
	return nil
}

func runPanelRestart(ctx context.Context, mode, transport, peer, pass string, hashes []string, device string, workers int, gw, blobURL string, minBytes int64, waitSec int) error {
	s, err := startSession(ctx, mode, transport, peer, pass, hashes, device, workers, gw, false)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.download(ctx, blobURL, minBytes, 30*time.Second); err != nil {
		return fmt.Errorf("pre-panel download: %w", err)
	}
	cmd := strings.TrimSpace(os.Getenv("WDTT_PANEL_RESTART_CMD"))
	if cmd == "" {
		cmd = "systemctl restart wdtt"
	}
	log.Printf("PANEL_RESTART_CMD=%s", cmd)
	t0 := time.Now()
	if out, err := exec.Command("bash", "-c", cmd).CombinedOutput(); err != nil {
		return fmt.Errorf("panel restart: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	// Soft-restart local core (simulates client auto soft after workers die / panel back).
	deadline := time.Now().Add(time.Duration(waitSec) * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := s.softRestartCore(ctx, transport, peer, pass, hashes, device, workers); err != nil {
			lastErr = err
			log.Printf("soft after panel: %v — retry", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if err := s.download(ctx, blobURL, minBytes, 30*time.Second); err != nil {
			lastErr = err
			log.Printf("download after panel soft: %v — retry", err)
			time.Sleep(5 * time.Second)
			continue
		}
		fmt.Printf("PANEL_RECOVER_SEC=%.1f\n", time.Since(t0).Seconds())
		return nil
	}
	return fmt.Errorf("panel recover timeout %ds: %v", waitSec, lastErr)
}

func runWorkersZero(ctx context.Context, mode, transport, peer, pass string, hashes []string, device string, workers int, gw, blobURL string, minBytes int64) error {
	// Same as soft_core_die: stop core (workers→0), soft bring-up, download.
	return runSoftCoreDie(ctx, mode, transport, peer, pass, hashes, device, workers, gw, blobURL, minBytes)
}

// --- session ---

type session struct {
	mode         string
	gw           string
	c            *core.Core
	cancelEvents context.CancelFunc
	workersReady atomic.Int32
	softRestarts atomic.Int32
	tunIP        string
	rawDev       tun.Device
}

func (s *session) close() {
	if s.cancelEvents != nil {
		s.cancelEvents()
	}
	if s.c != nil {
		s.c.Stop()
		s.c = nil
	}
	stopBridge()
	if s.rawDev != nil {
		_ = s.rawDev.Close()
		s.rawDev = nil
	}
	if s.mode == "raw" {
		cleanupRawNS()
	} else {
		cleanupWGNS()
	}
}

func startSession(ctx context.Context, mode, transport, peer, pass string, hashes []string, device string, workers int, gw string, tunReady bool) (*session, error) {
	if mode == "raw" {
		return startRAW(ctx, transport, peer, pass, hashes, device, workers, gw, tunReady)
	}
	return startWG(ctx, transport, peer, pass, hashes, device, workers, gw, tunReady)
}

func startRAW(ctx context.Context, transport, peer, pass string, hashes []string, device string, workers int, gw string, tunReady bool) (*session, error) {
	cleanupRawNS()
	s := &session{mode: "raw", gw: gw}
	listen := "127.0.0.1:" + rawListen
	cfg := core.Config{
		PeerAddr: peer, Password: pass, Hashes: hashes,
		Listen: listen, DeviceID: device, Workers: workers, MTU: 1280,
		ObfsMode: "audio", TunnelMode: "raw", TurnTransport: transport,
		TunAlreadyReady: tunReady,
	}
	c := core.New(cfg)
	events, err := c.Start()
	if err != nil {
		return nil, err
	}
	s.c = c
	evCtx, cancel := context.WithCancel(ctx)
	s.cancelEvents = cancel
	ready := make(chan string, 1)
	go s.pumpEvents(evCtx, events, ready, "raw_config")

	var conf string
	select {
	case conf = <-ready:
	case <-time.After(90 * time.Second):
		s.close()
		return nil, fmt.Errorf("timeout raw_config")
	case <-ctx.Done():
		s.close()
		return nil, ctx.Err()
	}
	ip, mtu, err := parseRawIP(conf)
	if err != nil {
		s.close()
		return nil, err
	}
	s.tunIP = ip
	dev, err := tun.CreateTUN(rawIface, mtu)
	if err != nil {
		s.close()
		return nil, err
	}
	s.rawDev = dev
	if err := moveRawToNS(ip, mtu, gw); err != nil {
		s.close()
		return nil, err
	}
	if err := startBridge(dev, listen); err != nil {
		s.close()
		return nil, err
	}
	c.NotifyTunReady()
	if err := s.waitWorkers(workers, 60*time.Second); err != nil {
		log.Printf("WARN workers: %v", err)
	}
	time.Sleep(3 * time.Second)
	fmt.Printf("SESSION_READY mode=raw ip=%s\n", ip)
	return s, nil
}

func startWG(ctx context.Context, transport, peer, pass string, hashes []string, device string, workers int, gw string, tunReady bool) (*session, error) {
	cleanupWGNS()
	s := &session{mode: "wg", gw: gw}
	must(run("ip", "netns", "add", wgNS))
	must(run("ip", "link", "add", vethHost, "type", "veth", "peer", "name", vethNS))
	must(run("ip", "addr", "add", vethHostIP+"/30", "dev", vethHost))
	must(run("ip", "link", "set", vethHost, "up"))
	must(run("ip", "link", "set", vethNS, "netns", wgNS))
	must(nsWG("ip", "addr", "add", vethNSIP+"/30", "dev", vethNS))
	must(nsWG("ip", "link", "set", vethNS, "up"))
	must(nsWG("ip", "link", "set", "lo", "up"))
	must(nsWG("ip", "route", "add", vethHostIP+"/32", "dev", vethNS))

	listen := vethHostIP + ":" + wgListen
	cfg := core.Config{
		PeerAddr: peer, Password: pass, Hashes: hashes,
		Listen: listen, DeviceID: device, Workers: workers, MTU: 1280,
		ObfsMode: "audio", TunnelMode: "wg", TurnTransport: transport,
		TunAlreadyReady: tunReady,
	}
	c := core.New(cfg)
	events, err := c.Start()
	if err != nil {
		s.close()
		return nil, err
	}
	s.c = c
	evCtx, cancel := context.WithCancel(ctx)
	s.cancelEvents = cancel
	ready := make(chan string, 1)
	go s.pumpEvents(evCtx, events, ready, "wg_config")

	var conf string
	select {
	case conf = <-ready:
	case <-time.After(90 * time.Second):
		s.close()
		return nil, fmt.Errorf("timeout wg_config")
	case <-ctx.Done():
		s.close()
		return nil, ctx.Err()
	}
	addr, mtu, wgConf := parseWG(conf)
	wgConf = rewriteEndpoint(wgConf, listen)
	tmp, err := os.CreateTemp("", "soft-wg-*.conf")
	if err != nil {
		s.close()
		return nil, err
	}
	_, _ = tmp.WriteString(wgConf)
	tmp.Close()
	_ = os.Chmod(tmp.Name(), 0644)
	defer os.Remove(tmp.Name())

	must(run("ip", "link", "add", wgIface, "type", "wireguard"))
	must(run("wg", "setconf", wgIface, tmp.Name()))
	must(run("ip", "link", "set", wgIface, "netns", wgNS))
	if !strings.Contains(addr, "/") {
		addr += "/32"
	}
	must(nsWG("ip", "addr", "add", addr, "dev", wgIface))
	if mtu != "" {
		_ = nsWG("ip", "link", "set", wgIface, "mtu", mtu)
	}
	must(nsWG("ip", "link", "set", wgIface, "up"))
	must(nsWG("ip", "route", "add", "default", "dev", wgIface))
	s.tunIP = strings.Split(addr, "/")[0]
	c.NotifyTunReady()
	if err := s.waitWorkers(workers, 60*time.Second); err != nil {
		log.Printf("WARN workers: %v", err)
	}
	time.Sleep(8 * time.Second)
	// Warm path to gateway before HTTP probe.
	_ = nsWG("ping", "-c", "2", "-W", "2", gw)
	fmt.Printf("SESSION_READY mode=wg ip=%s\n", s.tunIP)
	return s, nil
}

func (s *session) softRestartCore(ctx context.Context, transport, peer, pass string, hashes []string, device string, workers int) error {
	s.softRestarts.Add(1)
	if s.cancelEvents != nil {
		s.cancelEvents()
	}
	if s.c != nil {
		s.c.Stop()
		s.c = nil
	}
	time.Sleep(2 * time.Second)

	var listen string
	var tunnelMode string
	var evName string
	if s.mode == "raw" {
		listen = "127.0.0.1:" + rawListen
		tunnelMode = "raw"
		evName = "raw_config"
		stopBridge()
	} else {
		listen = vethHostIP + ":" + wgListen
		tunnelMode = "wg"
		evName = "wg_config"
	}
	cfg := core.Config{
		PeerAddr: peer, Password: pass, Hashes: hashes,
		// Same device_id on soft — иначе FATAL_AUTH «пароль привязан к другому устройству».
		Listen: listen, DeviceID: device,
		Workers: workers, MTU: 1280, ObfsMode: "audio",
		TunnelMode: tunnelMode, TurnTransport: transport, TunAlreadyReady: true,
		RawPrimaryIP: s.tunIP,
	}
	c := core.New(cfg)
	events, err := c.Start()
	if err != nil {
		return err
	}
	s.c = c
	evCtx, cancel := context.WithCancel(ctx)
	s.cancelEvents = cancel
	ready := make(chan string, 1)
	s.workersReady.Store(0)
	go s.pumpEvents(evCtx, events, ready, evName)

	select {
	case conf := <-ready:
		if s.mode == "raw" {
			ip, _, perr := parseRawIP(conf)
			if perr == nil && ip != "" && ip != s.tunIP {
				log.Printf("soft raw_config IP=%s (tun=%s) — rewriting netns addr", ip, s.tunIP)
				_ = run("ip", "netns", "exec", rawNS, "ip", "addr", "flush", "dev", rawIface)
				_ = run("ip", "netns", "exec", rawNS, "ip", "addr", "add", ip+"/16", "dev", rawIface)
				_ = run("ip", "netns", "exec", rawNS, "ip", "route", "replace", "default", "dev", rawIface)
				s.tunIP = ip
			}
			if err := startBridge(s.rawDev, listen); err != nil {
				return err
			}
		} else {
			_, _, wgConf := parseWG(conf)
			wgConf = rewriteEndpoint(wgConf, listen)
			tmp, err := os.CreateTemp("", "soft-wg-re-*.conf")
			if err != nil {
				return err
			}
			_, _ = tmp.WriteString(wgConf)
			tmp.Close()
			_ = os.Chmod(tmp.Name(), 0644)
			defer os.Remove(tmp.Name())
			// setconf inside netns
			if err := nsWG("wg", "setconf", wgIface, tmp.Name()); err != nil {
				// copy conf into ns via host path (usually visible)
				if err2 := run("ip", "netns", "exec", wgNS, "wg", "setconf", wgIface, tmp.Name()); err2 != nil {
					return fmt.Errorf("wg setconf soft: %v / %v", err, err2)
				}
			}
		}
	case <-time.After(90 * time.Second):
		return fmt.Errorf("timeout %s after soft", evName)
	case <-ctx.Done():
		return ctx.Err()
	}
	c.NotifyTunReady()
	_ = s.waitWorkers(workers, 60*time.Second)
	time.Sleep(3 * time.Second)
	return nil
}

func (s *session) pumpEvents(ctx context.Context, events <-chan core.Event, ready chan<- string, want string) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			switch ev.Type {
			case core.EventLog:
				if strings.Contains(ev.Message, "READY") {
					s.workersReady.Add(1)
					log.Printf("[%s] %s", ev.Level, ev.Message)
				}
			case core.EventEvent:
				if ev.Name == want {
					select {
					case ready <- ev.Data:
					default:
					}
				}
			case core.EventStats:
				if int(ev.Workers) > int(s.workersReady.Load()) {
					s.workersReady.Store(ev.Workers)
				}
			case core.EventError:
				log.Printf("[ERR] %s", ev.Message)
			}
		}
	}
}

func (s *session) waitWorkers(need int, d time.Duration) error {
	if need > 7 {
		need = 7
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if int(s.workersReady.Load()) >= need {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("only %d/%d workers", s.workersReady.Load(), need)
}

func (s *session) download(ctx context.Context, url string, minBytes int64, maxWait time.Duration) error {
	ns := rawNS
	if s.mode == "wg" {
		ns = wgNS
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		args := []string{"netns", "exec", ns, "curl", "-4", "-sS", "-o", "/dev/null",
			"-w", "%{speed_download} %{size_download} %{http_code}\n",
			"--max-time", strconv.Itoa(int(maxWait.Seconds())),
			"--connect-timeout", "15", url}
		cmd := exec.CommandContext(ctx, "ip", args...)
		out, err := cmd.CombinedOutput()
		line := strings.TrimSpace(string(out))
		fmt.Printf("DOWNLOAD attempt=%d %s\n", attempt, line)
		var bps, size float64
		var code int
		fmt.Sscanf(line, "%f %f %d", &bps, &size, &code)
		if err == nil && code == 200 && int64(size) >= minBytes {
			fmt.Printf("DOWNLOAD_OK mbit=%.2f bytes=%.0f\n", bps*8/1e6, size)
			return nil
		}
		lastErr = fmt.Errorf("download fail err=%v out=%s min=%d", err, line, minBytes)
		time.Sleep(time.Duration(attempt) * 2 * time.Second)
	}
	return lastErr
}

// --- helpers ---

func cleanupRawNS() {
	stopBridge()
	_ = exec.Command("ip", "netns", "del", rawNS).Run()
	_ = exec.Command("ip", "link", "del", rawIface).Run()
}

func cleanupWGNS() {
	_ = exec.Command("ip", "netns", "del", wgNS).Run()
	_ = exec.Command("ip", "link", "del", vethHost).Run()
	_ = exec.Command("ip", "link", "del", wgIface).Run()
}

func moveRawToNS(ip string, mtu int, gw string) error {
	_ = run("ip", "netns", "del", rawNS)
	if err := run("ip", "netns", "add", rawNS); err != nil {
		return err
	}
	if err := run("ip", "link", "set", rawIface, "netns", rawNS); err != nil {
		return err
	}
	ns := func(a ...string) error { return run(append([]string{"ip", "netns", "exec", rawNS}, a...)...) }
	if err := ns("ip", "addr", "add", ip+"/16", "dev", rawIface); err != nil {
		return err
	}
	_ = ns("ip", "link", "set", rawIface, "mtu", strconv.Itoa(mtu))
	if err := ns("ip", "link", "set", rawIface, "up"); err != nil {
		return err
	}
	_ = ns("ip", "link", "set", "lo", "up")
	_ = gw
	return ns("ip", "route", "add", "default", "dev", rawIface)
}

func nsWG(args ...string) error {
	return run(append([]string{"ip", "netns", "exec", wgNS}, args...)...)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func run(args ...string) error {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s (%w)", args, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func parseRawIP(conf string) (ip string, mtu int, err error) {
	mtu = 1280
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "IP") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				ip = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(line, "MTU") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				if n, e := strconv.Atoi(strings.TrimSpace(parts[1])); e == nil {
					mtu = n
				}
			}
		}
	}
	if ip == "" {
		return "", 0, fmt.Errorf("no IP in config")
	}
	return ip, mtu, nil
}

func parseWG(conf string) (addr, mtu, wgConf string) {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(conf))
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		parts := strings.SplitN(t, "=", 2)
		if len(parts) == 2 {
			k := strings.ToLower(strings.TrimSpace(parts[0]))
			v := strings.TrimSpace(parts[1])
			switch k {
			case "address":
				addr = v
				continue
			case "mtu":
				mtu = v
				continue
			case "dns", "table", "preup", "postup", "predown", "postdown", "saveconfig":
				continue
			}
		}
		b.WriteString(line + "\n")
	}
	return addr, mtu, b.String()
}

func rewriteEndpoint(conf, ep string) string {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(conf))
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(t), "endpoint") {
			b.WriteString("Endpoint = " + ep + "\n")
			continue
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func env(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}

func splitCSV(s string) []string {
	var o []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			o = append(o, p)
		}
	}
	return o
}

var (
	bridgeStop chan struct{}
)

func startBridge(dev tun.Device, listen string) error {
	stopBridge()
	if dev == nil {
		return fmt.Errorf("nil tun")
	}
	host, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	port, _ := strconv.Atoi(portStr)
	uc, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(host), Port: port})
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	bridgeStop = stop
	go func() {
		bufs := [][]byte{make([]byte, 2048)}
		sizes := make([]int, 1)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := dev.Read(bufs, sizes, 0)
			if err != nil {
				select {
				case <-stop:
					return
				default:
					time.Sleep(5 * time.Millisecond)
					continue
				}
			}
			for i := 0; i < n; i++ {
				sz := sizes[i]
				if sz < 20 {
					continue
				}
				pkt := bufs[i][:sz]
				if pkt[0]>>4 != 4 {
					continue
				}
				_, _ = uc.Write(pkt)
			}
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			_ = uc.SetReadDeadline(time.Now().Add(2 * time.Second))
			nr, err := uc.Read(buf)
			if err != nil {
				select {
				case <-stop:
					_ = uc.Close()
					return
				default:
					continue
				}
			}
			if nr < 20 || buf[0]>>4 != 4 {
				continue
			}
			pkt := buf[:nr]
			packet := make([]byte, 16+len(pkt))
			copy(packet[16:], pkt)
			_, _ = dev.Write([][]byte{packet}, 16)
		}
	}()
	_, _ = uc.Write([]byte{0x00})
	return nil
}

func stopBridge() {
	if bridgeStop != nil {
		close(bridgeStop)
		bridgeStop = nil
	}
}
