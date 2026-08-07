package core

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cbeuw/connutil"
	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	"github.com/pion/logging"
)

// LIVE_RAW=1 go test ./core -run LiveRawConf -count=1
func TestLiveRawConfAgainstLocal(t *testing.T) {
	if os.Getenv("LIVE_RAW") == "" {
		t.Skip("set LIVE_RAW=1")
	}
	addr := envOr("WDTT_PEER", "127.0.0.1:56000")
	pass := envOr("WDTT_PASS", "ildar")

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

	conf, err := RequestRawConfig(dtlsConn, "live-probe", pass, 1280)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conf, "IP = 10.70.66.") {
		t.Fatalf("unexpected conf: %q", conf)
	}
	t.Logf("RAW OK:\n%s", conf)
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
