// wdtt-bench — замер реальной скорости RAW через VK TURN.
// Клиентский TUN в network namespace (не конфликтует с wdtt-raw на том же хосте).
//
//	WDTT_PASS=... WDTT_HASHES=h1,h2 WDTT_PEER=host:56000 \
//	  ./wdtt-bench -transport tcp -workers 9
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	benchIface = "wdtt-bench"
	benchNS    = "wdttbench"
	listenPort = "19000"
)

func main() {
	transport := flag.String("transport", "tcp", "tcp|udp")
	workers := flag.Int("workers", 9, "workers")
	seconds := flag.Int("sec", 15, "download seconds")
	mb := flag.Int("mb", 20, "download size MB")
	urlFlag := flag.String("url", "", "download URL (default: local gateway blob or cloudflare)")
	local := flag.Bool("local", true, "download from 10.70.66.1 (tunnel-only, no internet)")
	hold := flag.Int("hold", 0, "keep tunnel up N seconds after ready (0=run download)")
	flag.Parse()

	pass := env("WDTT_PASS", "")
	peer := env("WDTT_PEER", "94.242.53.211:56000")
	hashes := splitCSV(env("WDTT_HASHES", ""))
	device := env("WDTT_DEVICE", "bench-ns-"+strconv.FormatInt(time.Now().Unix()%100000, 10))
	cfgRoot := env("WDTT_CFG", "/tmp/pwdtt-bench-cfg")
	if pass == "" || len(hashes) == 0 {
		log.Fatal("need WDTT_PASS and WDTT_HASHES")
	}
	core.SetConfigRoot(cfgRoot)

	var dlURL string
	var targets []string
	if *urlFlag != "" {
		dlURL = *urlFlag
		host := hostOf(dlURL)
		if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
			targets = []string{ip.String()}
		} else {
			var err error
			targets, err = resolveV4(host)
			if err != nil {
				log.Fatal(err)
			}
		}
	} else if *local {
		dlURL = fmt.Sprintf("http://10.70.66.1:18080/blob?bytes=%d", (*mb)*1024*1024)
		targets = []string{"10.70.66.1"}
	} else {
		dlURL = fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", (*mb)*1024*1024)
		var err error
		targets, err = resolveV4("speed.cloudflare.com")
		if err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("targets %v url=%s", targets, dlURL)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cleanupNS()
	defer cleanupNS()

	cfg := core.Config{
		PeerAddr:      peer,
		Password:      pass,
		Hashes:        hashes,
		Listen:        "127.0.0.1:" + listenPort,
		DeviceID:      device,
		Workers:       *workers,
		MTU:           1280,
		ObfsMode:      "audio",
		TunnelMode:    "raw",
		TurnTransport: *transport,
	}
	c := core.New(cfg)
	events, err := c.Start()
	if err != nil {
		log.Fatalf("core: %v", err)
	}
	defer c.Stop()

	ready := make(chan string, 1)
	go func() {
		for ev := range events {
			switch ev.Type {
			case core.EventLog:
				msg := ev.Message
				if strings.Contains(msg, "READY") || strings.Contains(msg, "TURN ") || strings.Contains(msg, "ошиб") {
					log.Printf("[%s] %s", ev.Level, msg)
				}
			case core.EventError:
				log.Printf("[ERR] %s", ev.Message)
			case core.EventEvent:
				if ev.Name == "raw_config" {
					select {
					case ready <- ev.Data:
					default:
					}
				}
			case core.EventState:
				log.Printf("[STATE] %s", ev.Status)
			}
		}
	}()

	var conf string
	select {
	case conf = <-ready:
	case <-time.After(90 * time.Second):
		log.Fatal("timeout waiting raw_config")
	case <-ctx.Done():
		return
	}

	ip, mtu, err := parseRawIP(conf)
	if err != nil {
		log.Fatal(err)
	}
	dev, err := tun.CreateTUN(benchIface, mtu)
	if err != nil {
		log.Fatal(err)
	}
	// Сначала netns: иначе Read-горутина bridge умирает на ENODEV при move.
	if err := moveTUNToNetNS(ip, mtu, targets, c.GetTurnIPs()); err != nil {
		_ = dev.Close()
		log.Fatal(err)
	}
	if err := startBridge(dev); err != nil {
		log.Fatal(err)
	}
	defer stopBridge()
	c.NotifyTunReady()
	log.Printf("TUN in netns %s ip=%s; waiting workers…", benchNS, ip)
	// Ждём пока в netns реально есть iface + хотя бы 3 воркера по логам не отследим — sleep + verify.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if err := run("ip", "netns", "exec", benchNS, "ip", "link", "show", benchIface); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := run("ip", "netns", "exec", benchNS, "ip", "link", "show", benchIface); err != nil {
		log.Fatalf("netns/iface missing after setup: %v", err)
	}
	time.Sleep(15 * time.Second)
	fmt.Printf("BENCH_READY ip=%s transport=%s workers=%d\n", ip, *transport, *workers)
	os.Stdout.Sync()

	if *hold > 0 {
		log.Printf("HOLD %ds (tunnel up for external downlink bench)", *hold)
		// Не вызываем Stop до конца hold — иначе netns/TUN схлопнется.
		t := time.NewTimer(time.Duration(*hold) * time.Second)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
		}
		fmt.Println("BENCH_HOLD_DONE")
		return
	}

	log.Printf("=== DOWNLOAD transport=%s workers=%d ===", *transport, *workers)
	mbps, n, err := downloadInNS(ctx, dlURL, time.Duration(*seconds)*time.Second)
	if err != nil {
		log.Printf("download err: %v (got %.2f Mbit/s, %d bytes)", err, mbps, n)
	} else {
		log.Printf("RESULT download=%.2f Mbit/s bytes=%d", mbps, n)
	}
	fmt.Printf("BENCH_RESULT transport=%s workers=%d download_mbit=%.2f bytes=%d\n",
		*transport, *workers, mbps, n)
}

func hostOf(raw string) string {
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		raw = raw[:i]
	}
	if h, _, err := net.SplitHostPort(raw); err == nil {
		return h
	}
	return raw
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func resolveV4(host string) ([]string, error) {
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no A records for %s", host)
	}
	return out, nil
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

func run(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s (%w)", args, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func cleanupNS() {
	stopBridge()
	_ = exec.Command("ip", "netns", "del", benchNS).Run()
	_ = exec.Command("ip", "link", "del", benchIface).Run()
}

func moveTUNToNetNS(ip string, mtu int, targets, turnIPs []string) error {
	_ = run("ip", "netns", "del", benchNS)
	if err := run("ip", "netns", "add", benchNS); err != nil {
		return err
	}
	if err := run("ip", "link", "set", benchIface, "netns", benchNS); err != nil {
		return err
	}
	ns := func(args ...string) error {
		return run(append([]string{"ip", "netns", "exec", benchNS}, args...)...)
	}
	if err := ns("ip", "addr", "add", ip+"/16", "dev", benchIface); err != nil {
		return err
	}
	_ = ns("ip", "link", "set", benchIface, "mtu", strconv.Itoa(mtu))
	if err := ns("ip", "link", "set", benchIface, "up"); err != nil {
		return err
	}
	_ = ns("ip", "link", "set", "lo", "up")
	// default via TUN
	if err := ns("ip", "route", "add", "default", "dev", benchIface); err != nil {
		return err
	}
	// DNS via TCP to 1.1.1.1 through tunnel — or static resolve only (we use IPs in curl)
	_ = targets
	_ = turnIPs
	return nil
}

func downloadInNS(ctx context.Context, rawURL string, maxWait time.Duration) (float64, int64, error) {
	args := []string{"netns", "exec", benchNS, "curl", "-4", "-sS", "-o", "/dev/null",
		"-w", "\n%{speed_download}\n", "--max-time", strconv.Itoa(int(maxWait.Seconds())),
		"--connect-timeout", "10", rawURL}
	cmd := exec.CommandContext(ctx, "ip", args...)
	out, err := cmd.CombinedOutput()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	speedLine := lines[len(lines)-1]
	bps, parseErr := strconv.ParseFloat(strings.TrimSpace(speedLine), 64)
	if parseErr != nil {
		return 0, 0, fmt.Errorf("curl: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	mbps := bps * 8 / 1e6
	// bytes ≈ из текста curl "with N out of"
	var got int64
	for _, line := range lines {
		if i := strings.Index(line, "with "); i >= 0 {
			fmt.Sscanf(line[i:], "with %d ", &got)
		}
	}
	if got == 0 {
		got = int64(bps * maxWait.Seconds())
	}
	return mbps, got, err
}

var (
	bridgeStop chan struct{}
	bridgeDev  tun.Device
	bridgeUpN  atomic.Uint64
	bridgeDnN  atomic.Uint64
)

func startBridge(dev tun.Device) error {
	stopBridge()
	uc, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: mustPort(listenPort)})
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	bridgeStop = stop
	bridgeDev = dev
	bridgeUpN.Store(0)
	bridgeDnN.Store(0)
	go func() {
		bufs := [][]byte{make([]byte, 2048)}
		sizes := make([]int, 1)
		// offset=0: wireguard-go кладёт IPv4 в buf[offset:]; с vnetHdr hdr снимается внутри.
		const tunOff = 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := dev.Read(bufs, sizes, tunOff)
			if err != nil {
				select {
				case <-stop:
					return
				default:
					time.Sleep(20 * time.Millisecond)
					continue
				}
			}
			for i := 0; i < n; i++ {
				sz := sizes[i]
				if sz < 20 {
					continue
				}
				pkt := bufs[i][tunOff : tunOff+sz]
				if pkt[0]>>4 != 4 {
					continue
				}
				if _, werr := uc.Write(pkt); werr == nil {
					bridgeUpN.Add(1)
				}
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
			if _, werr := dev.Write([][]byte{packet}, 16); werr == nil {
				bridgeDnN.Add(1)
			}
		}
	}()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				log.Printf("[BRIDGE] up=%d dn=%d", bridgeUpN.Swap(0), bridgeDnN.Swap(0))
			}
		}
	}()
	// Probe: зарегистрировать clientAddr в Core до любого TUN uplink.
	// Иначе server→client (ping/nc) дропается в writeLoop.
	_, _ = uc.Write([]byte{0x00})
	_ = io.Discard
	return nil
}

func stopBridge() {
	if bridgeStop != nil {
		close(bridgeStop)
		bridgeStop = nil
	}
	if bridgeDev != nil {
		_ = bridgeDev.Close()
		bridgeDev = nil
	}
}

func mustPort(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
