package core

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cbeuw/connutil"
	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	"github.com/pion/logging"
)

// LIVE_RAW=1 go test ./core -run LiveRawConf -count=1
// Direct PWDTT: LIVE_RAW=1 WDTT_RAW_DIRECT=1 …
// Direct qWDTT: LIVE_RAW=1 WDTT_RAW_DIRECT=1 WDTT_RAW_QWDTT=1 …
func TestLiveRawConfAgainstLocal(t *testing.T) {
	if os.Getenv("LIVE_RAW") == "" {
		t.Skip("set LIVE_RAW=1")
	}
	addr := envOr("WDTT_PEER", "127.0.0.1:56000")
	pass := envOr("WDTT_PASS", "ildar")
	deviceID := envOr("WDTT_DEVICE", "live-probe")

	key, err := deriveWrapKey(pass)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.DialUDP("udp", nil, peer)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()

	pipeA, pipeB := connutil.AsyncPacketPipe()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	obfsCfg := NewObfsConfig("audio")
	obfsWrite := NewObfsState()
	plain := make([]byte, 2048)

	go func() {
		buf := make([]byte, 2048)
		for {
			n, _, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			payload := buf[:n]
			if !obfsIsRTPPacket(payload) {
				continue
			}
			m, err := obfsUnwrapPacket(key, payload, plain)
			if err != nil {
				continue
			}
			_, _ = pipeA.WriteTo(plain[:m], peer)
		}
	}()
	go func() {
		b := make([]byte, 2048)
		for {
			n, _, err := pipeA.ReadFrom(b)
			if err != nil {
				return
			}
			wrapped, err := obfsWrapPacket(key, b[:n], obfsCfg, obfsWrite)
			if err != nil {
				return
			}
			if _, err := udp.Write(wrapped); err != nil {
				return
			}
		}
	}()

	if os.Getenv("WDTT_RAW_DIRECT") != "" {
		rawConn := &liveUDP{PacketConn: pipeB, raddr: peer}
		var conf string
		var source net.IP
		if os.Getenv("WDTT_RAW_QWDTT") == "1" {
			conf, err = requestQWDTTRawConfig(rawConn, deviceID, pass)
			if err != nil {
				t.Fatal(err)
			}
			source, _, _, err = parseQWDTTRawConfResponse(conf)
			if err != nil {
				t.Fatalf("qWDTT RAWCONF parse: %v (resp=%q)", err, conf)
			}
			t.Logf("qWDTT GETCONF_RAW OK: %s", conf)
		} else {
			conf, err = RequestRawConfig(rawConn, deviceID, pass, 1280)
			if err != nil {
				t.Fatal(err)
			}
			source = parseRawConfIP(conf)
		}
		gateway := net.ParseIP(envOr("WDTT_RAW_GATEWAY", "10.70.66.1")).To4()
		if source == nil || gateway == nil {
			t.Fatalf("invalid RAW addresses: source=%v gateway=%v conf=%q", source, gateway, conf)
		}
		echo := buildLiveICMPEcho(source.To4(), gateway)
		if _, err := rawConn.Write(echo); err != nil {
			t.Fatalf("send RAW echo: %v", err)
		}
		if err := rawConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		reply := make([]byte, 2048)
		n, err := rawConn.Read(reply)
		if err != nil {
			t.Fatalf("read RAW echo: %v", err)
		}
		if n < 28 || reply[9] != 1 || reply[20] != 0 ||
			!net.IP(reply[12:16]).Equal(gateway) || !net.IP(reply[16:20]).Equal(source) {
			t.Fatalf("unexpected RAW echo reply: %x", reply[:n])
		}
		t.Logf("RAW direct traffic OK: %s -> %s -> %s", source, gateway, source)
		return
	}

	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	dtlsConn, err := dtls.Client(&liveUDP{PacketConn: pipeB, raddr: peer}, peer, &dtls.Config{
		Certificates:          []tls.Certificate{cert},
		InsecureSkipVerify:    true,
		ExtendedMasterSecret:  dtls.RequireExtendedMasterSecret,
		CipherSuites:          []dtls.CipherSuiteID{dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		ConnectionIDGenerator: dtls.OnlySendCIDGenerator(),
		FlightInterval:        100 * time.Millisecond,
		LoggerFactory:         logging.NewDefaultLoggerFactory(),
	})
	if err != nil {
		t.Fatalf("dtls: %v", err)
	}
	defer dtlsConn.Close()
	hctx, hcancel := context.WithTimeout(ctx, 15*time.Second)
	err = dtlsConn.HandshakeContext(hctx)
	hcancel()
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	conf, err := RequestRawConfig(dtlsConn, deviceID, pass, 1280)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conf, "IP = 10.70.") {
		t.Fatalf("unexpected conf: %q", conf)
	}
	t.Logf("RAW OK:\n%s", conf)
}

// requestQWDTTRawConfig — qWDTT Android 1.4 dialect: GETCONF_RAW → RAWCONF:ip|dns|mtu.
func requestQWDTTRawConfig(conn net.Conn, deviceID, password string) (string, error) {
	payload := fmt.Sprintf("GETCONF_RAW:%s|%s", deviceID, password)
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", fmt.Errorf("отправка GETCONF_RAW: %w", err)
	}
	b := make([]byte, 4096)
	if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return "", fmt.Errorf("установка дедлайна: %w", err)
	}
	n, err := conn.Read(b)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return "", fmt.Errorf("чтение ответа GETCONF_RAW: %w", err)
	}
	resp := string(b[:n])
	if strings.HasPrefix(resp, "DENIED:") {
		return "", fmt.Errorf("GETCONF_RAW denied: %s", strings.TrimPrefix(resp, "DENIED:"))
	}
	if resp == "NOCONF" {
		return "", fmt.Errorf("GETCONF_RAW: NOCONF")
	}
	if _, _, _, err := parseQWDTTRawConfResponse(resp); err != nil {
		return "", fmt.Errorf("неожиданный ответ GETCONF_RAW: %w (resp=%q)", err, trimProtoPreview(resp, 64))
	}
	return resp, nil
}

// parseQWDTTRawConfResponse parses RAWCONF:ip|dns|mtu (qWDTT pipe dialect).
func parseQWDTTRawConfResponse(resp string) (ip net.IP, dns string, mtu int, err error) {
	resp = strings.TrimSpace(resp)
	if !strings.HasPrefix(resp, "RAWCONF:") {
		return nil, "", 0, fmt.Errorf("want RAWCONF:ip|dns|mtu, got %q", trimProtoPreview(resp, 48))
	}
	body := strings.TrimSpace(strings.TrimPrefix(resp, "RAWCONF:"))
	parts := strings.Split(body, "|")
	if len(parts) < 3 {
		return nil, "", 0, fmt.Errorf("want 3 pipe fields, got %d in %q", len(parts), body)
	}
	ip = net.ParseIP(strings.TrimSpace(parts[0]))
	if ip4 := ip.To4(); ip4 == nil {
		return nil, "", 0, fmt.Errorf("bad ip %q", parts[0])
	} else {
		ip = ip4
	}
	dns = strings.TrimSpace(parts[1])
	if dns == "" {
		return nil, "", 0, fmt.Errorf("empty dns")
	}
	mtu, err = strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || mtu < 576 || mtu > 1500 {
		return nil, "", 0, fmt.Errorf("bad mtu %q", parts[2])
	}
	return ip, dns, mtu, nil
}

func TestParseQWDTTRawConfResponse(t *testing.T) {
	ip, dns, mtu, err := parseQWDTTRawConfResponse("RAWCONF:10.70.0.2|1.1.1.1|1280")
	if err != nil {
		t.Fatal(err)
	}
	if ip.String() != "10.70.0.2" || dns != "1.1.1.1" || mtu != 1280 {
		t.Fatalf("got %v %q %d", ip, dns, mtu)
	}
	if _, _, _, err := parseQWDTTRawConfResponse("IP = 10.70.0.2\nDNS = 1.1.1.1\n"); err == nil {
		t.Fatal("multiline PWDTT must not parse as qWDTT pipe")
	}
	if _, _, _, err := parseQWDTTRawConfResponse("RAWCONF:10.70.0.2|1.1.1.1"); err == nil {
		t.Fatal("need mtu field")
	}
	if _, _, _, err := parseQWDTTRawConfResponse("RAWCHAL:deadbeef"); err == nil {
		t.Fatal("RAWCHAL is not qWDTT success")
	}
}

func TestRequestQWDTTRawConfigPipe(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() {
		buf := make([]byte, 256)
		n, err := c2.Read(buf)
		if err != nil {
			return
		}
		if got := string(buf[:n]); got != "GETCONF_RAW:android|secret" {
			_, _ = c2.Write([]byte("DENIED:bad_request"))
			return
		}
		_, _ = c2.Write([]byte("RAWCONF:10.70.1.5|8.8.8.8|1280"))
	}()
	resp, err := requestQWDTTRawConfig(c1, "android", "secret")
	if err != nil {
		t.Fatal(err)
	}
	ip, dns, mtu, err := parseQWDTTRawConfResponse(resp)
	if err != nil || ip.String() != "10.70.1.5" || dns != "8.8.8.8" || mtu != 1280 {
		t.Fatalf("resp=%q ip=%v dns=%q mtu=%d err=%v", resp, ip, dns, mtu, err)
	}
}

func buildLiveICMPEcho(source, destination net.IP) []byte {
	pkt := make([]byte, 28)
	pkt[0] = 0x45
	pkt[2], pkt[3] = 0, byte(len(pkt))
	pkt[4], pkt[5] = 0x12, 0x34
	pkt[6] = 0x40
	pkt[8] = 64
	pkt[9] = 1
	copy(pkt[12:16], source.To4())
	copy(pkt[16:20], destination.To4())
	pkt[20] = 8
	pkt[24], pkt[25] = 0x43, 0x21
	pkt[26], pkt[27] = 0, 1
	writeLiveChecksum(pkt[20:28], 2)
	writeLiveChecksum(pkt[:20], 10)
	return pkt
}

func writeLiveChecksum(data []byte, offset int) {
	var sum uint32
	for i := 0; i+1 < len(data); i += 2 {
		if i == offset {
			continue
		}
		sum += uint32(data[i])<<8 | uint32(data[i+1])
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	value := ^uint16(sum)
	data[offset], data[offset+1] = byte(value>>8), byte(value)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type liveUDP struct {
	net.PacketConn
	raddr net.Addr
}

func (c *liveUDP) Read(b []byte) (int, error) {
	n, _, err := c.ReadFrom(b)
	return n, err
}
func (c *liveUDP) Write(b []byte) (int, error) { return c.WriteTo(b, c.raddr) }
func (c *liveUDP) RemoteAddr() net.Addr        { return c.raddr }
