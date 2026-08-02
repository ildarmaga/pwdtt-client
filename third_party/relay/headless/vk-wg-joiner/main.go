// Command vk-wg-joiner is a headless validation harness for the VK
// WireGuard+TURN → SOCKS5 bridge. It mirrors the PC client's connection but
// runs entirely in userspace (netstack), exposing a local SOCKS5 proxy.
//
// Example:
//
//	vk-wg-joiner -peer devgamemaga.mooo.com:56000 -pass 'secret' \
//	  -hash 'h1,h2,h3,h4' -socks 127.0.0.1:1080
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ildarmaga/whitelist-bypass/relay/vkwg"
)

func main() {
	var (
		peer    = flag.String("peer", "", "WG/DTLS server endpoint ip:dtlsPort")
		pass    = flag.String("pass", "", "connection password")
		hash    = flag.String("hash", "", "comma-separated VK TURN hashes")
		device  = flag.String("device", "linux-test", "device id")
		workers = flag.Int("n", 9, "TURN workers")
		mtu     = flag.Int("mtu", 1380, "WireGuard MTU")
		socks   = flag.String("socks", "127.0.0.1:1080", "SOCKS5 listen addr")
		udp     = flag.String("udp", "127.0.0.1:9000", "dispatcher UDP listen addr")
		captcha = flag.String("captcha-mode", "auto", "captcha mode: auto|rjs|wv")
	)
	flag.Parse()

	if *peer == "" || *pass == "" || *hash == "" {
		fmt.Fprintln(os.Stderr, "peer, pass and hash are required")
		flag.Usage()
		os.Exit(2)
	}

	hashes := splitHashes(*hash)
	log.Printf("[vk-wg] peer=%s workers=%d hashes=%d socks=%s", *peer, *workers, len(hashes), *socks)

	b := vkwg.New(vkwg.Config{
		PeerAddr:    *peer,
		Password:    *pass,
		Hashes:      hashes,
		DeviceID:    *device,
		Workers:     *workers,
		MTU:         *mtu,
		CaptchaMode: *captcha,
		UDPListen:   *udp,
		SocksListen: *socks,
		Log: func(level, msg string) {
			log.Printf("[%s] %s", level, msg)
		},
	})

	if err := b.Start(); err != nil {
		log.Fatalf("[vk-wg] start failed: %v", err)
	}
	log.Printf("[vk-wg] READY — SOCKS5 proxy on %s", *socks)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("[vk-wg] shutting down")
	b.Stop()
}

func splitHashes(raw string) []string {
	var out []string
	for _, h := range strings.Split(raw, ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}
