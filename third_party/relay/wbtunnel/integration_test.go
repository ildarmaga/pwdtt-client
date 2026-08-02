package wbtunnel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
)

// pipeTunnel is a minimal in-memory DataTunnel for wbtunnel integration tests.
type pipeTunnel struct {
	peer *pipeTunnel

	mu            sync.Mutex
	onData        func([]byte)
	onPeerRestart func()
}

func newPipePair() (a, b *pipeTunnel) {
	a = &pipeTunnel{}
	b = &pipeTunnel{}
	a.peer = b
	b.peer = a
	return a, b
}

func (p *pipeTunnel) SendData(data []byte) {
	if p.peer == nil || len(data) == 0 {
		return
	}
	cp := append([]byte(nil), data...)
	p.peer.deliver(cp)
}

func (p *pipeTunnel) SendRaw(data []byte) { p.SendData(data) }

func (p *pipeTunnel) deliver(data []byte) {
	p.mu.Lock()
	fn := p.onData
	p.mu.Unlock()
	if fn != nil {
		go fn(data)
	}
}

func (p *pipeTunnel) SetOnData(fn func([]byte)) {
	p.mu.Lock()
	p.onData = fn
	p.mu.Unlock()
}

func (p *pipeTunnel) SetOnClose(func()) {}

func (p *pipeTunnel) Reconfigure(int, int) {}

func (p *pipeTunnel) SetOnPeerRestart(fn func()) {
	p.mu.Lock()
	p.onPeerRestart = fn
	p.mu.Unlock()
}

func (p *pipeTunnel) triggerPeerRestart() {
	p.mu.Lock()
	fn := p.onPeerRestart
	p.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func startAcceptCloseServer(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr()
}

func waitKCP(t *testing.T) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
}

func smuxConnect(t *testing.T, j *Joiner, target net.Addr) {
	t.Helper()
	j.mu.Lock()
	sess := j.smuxSess
	j.mu.Unlock()
	if sess == nil {
		t.Fatal("joiner smux session nil")
	}

	stream, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer stream.Close()

	host, portStr, err := net.SplitHostPort(target.String())
	if err != nil {
		t.Fatalf("split target addr: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	req, err := json.Marshal(ConnectRequest{
		Cmd:  connectCommand,
		Addr: host,
		Port: port,
	})
	if err != nil {
		t.Fatalf("marshal connect: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	_ = stream.SetDeadline(deadline)
	if _, err := stream.Write(req); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	ack := make([]byte, 1)
	if _, err := io.ReadFull(stream, ack); err != nil || ack[0] != 0x00 {
		t.Fatalf("connect ack: %v ack=%v", err, ack)
	}
}

func skipIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("wbtunnel integration skipped in CI (-short)")
	}
}

func TestWBTSmuxConnect(t *testing.T) {
	skipIntegration(t)
	creatorTun, joinerTun := newPipePair()
	echoAddr := startAcceptCloseServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	waitKCP(t)
	smuxConnect(t, joiner, echoAddr)
}

func TestWBTJoinerSwapCreatorResync(t *testing.T) {
	skipIntegration(t)
	creatorTun, joinerTun := newPipePair()
	echoAddr := startAcceptCloseServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	waitKCP(t)
	smuxConnect(t, joiner, echoAddr)

	if err := joiner.SwapTunnel(joinerTun, nil); err != nil {
		t.Fatalf("joiner swap: %v", err)
	}

	creatorTun.triggerPeerRestart()
	joinerTun.triggerPeerRestart()
	waitKCP(t)
	smuxConnect(t, joiner, echoAddr)
}

func TestSwapTunnelDebounce(t *testing.T) {
	skipIntegration(t)
	creatorTun, joinerTun := newPipePair()
	echoAddr := startAcceptCloseServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	waitKCP(t)
	smuxConnect(t, joiner, echoAddr)

	joiner.mu.Lock()
	sess1 := joiner.smuxSess
	joiner.mu.Unlock()

	if err := joiner.SwapTunnel(joinerTun, nil); err != nil {
		t.Fatalf("first swap: %v", err)
	}
	joiner.mu.Lock()
	sess2 := joiner.smuxSess
	joiner.mu.Unlock()
	if sess2 == nil || sess2 == sess1 {
		t.Fatal("first SwapTunnel should replace smux session")
	}

	// Immediate second swap (deferred sub-offer race) must be a no-op.
	if err := joiner.SwapTunnel(joinerTun, nil); err != nil {
		t.Fatalf("debounced swap: %v", err)
	}
	joiner.mu.Lock()
	sess3 := joiner.smuxSess
	joiner.mu.Unlock()
	if sess3 != sess2 {
		t.Fatal("debounced SwapTunnel must keep the same smux session")
	}

	if err := creator.SwapTunnel(creatorTun, nil); err != nil {
		t.Fatalf("creator first swap: %v", err)
	}
	if err := creator.SwapTunnel(creatorTun, nil); err != nil {
		t.Fatalf("creator debounced swap: %v", err)
	}

	waitKCP(t)
	smuxConnect(t, joiner, echoAddr)
}

func TestWBTUDPPoolMultiplex(t *testing.T) {
	skipIntegration(t)
	creatorTun, joinerTun := newPipePair()

	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := ln.ReadFrom(buf)
			if err != nil {
				return
			}
			if n > 0 && string(buf[:n]) == "ping" {
				_, _ = ln.WriteTo([]byte("pong"), addr)
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	waitKCP(t)

	host, portStr, _ := net.SplitHostPort(ln.LocalAddr().String())
	port, _ := strconv.Atoi(portStr)

	for i := 0; i < 5; i++ {
		resp, err := joiner.tunnelUDP(host, port, []byte("ping"))
		if err != nil {
			t.Fatalf("tunnelUDP #%d: %v", i, err)
		}
		if string(resp) != "pong" {
			t.Fatalf("tunnelUDP #%d: got %q", i, resp)
		}
	}

	joiner.udpPool.mu.Lock()
	n := len(joiner.udpPool.items)
	joiner.udpPool.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 pooled udp stream, got %d", n)
	}
}

func TestSharedUDPRelayPort(t *testing.T) {
	skipIntegration(t)
	creatorTun, joinerTun := newPipePair()

	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := ln.ReadFrom(buf)
			if err != nil {
				return
			}
			if n > 0 {
				_, _ = ln.WriteTo(buf[:n], addr)
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	creator, err := NewCreator(ctx, creatorTun, "", "", "", func(string, ...any) {}, nil, nil)
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	t.Cleanup(func() { creator.Close() })

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("socks listen: %v", err)
	}
	go func() { _ = joiner.ServeSOCKS(socksLn) }()
	t.Cleanup(func() { _ = socksLn.Close() })

	waitKCP(t)

	port1, hold1 := socksUDPAssociate(t, socksLn.Addr().String())
	port2, hold2 := socksUDPAssociate(t, socksLn.Addr().String())
	if port1 != port2 {
		t.Fatalf("expected same relay port, got %d and %d", port1, port2)
	}

	host, portStr, _ := net.SplitHostPort(ln.LocalAddr().String())
	dstPort, _ := strconv.Atoi(portStr)
	payload := []byte("dns-query")
	hdr := socksUDPHeaderIPv4(host, dstPort)
	udpLn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("client udp: %v", err)
	}
	t.Cleanup(func() { _ = udpLn.Close() })
	relayAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port1)))
	if err != nil {
		t.Fatalf("relay addr: %v", err)
	}
	if _, err := udpLn.WriteTo(append(hdr, payload...), relayAddr); err != nil {
		t.Fatalf("write relay: %v", err)
	}
	_ = hold1
	_ = hold2
}

func socksUDPAssociate(t *testing.T, socksAddr string) (relayPort int, hold net.Conn) {
	t.Helper()
	conn, err := net.Dial("tcp", socksAddr)
	if err != nil {
		t.Fatalf("dial socks: %v", err)
	}
	if _, err := conn.Write([]byte{common.Ver, 1, common.AuthNone}); err != nil {
		t.Fatalf("auth methods: %v", err)
	}
	ack := make([]byte, 2)
	if _, err := io.ReadFull(conn, ack); err != nil || ack[1] != common.AuthNone {
		t.Fatalf("auth ack: %v %v", err, ack)
	}
	assoc := []byte{common.Ver, common.CmdUDP, 0x00, common.AtypIPv4, 0, 0, 0, 0, 0, 0}
	if _, err := conn.Write(assoc); err != nil {
		t.Fatalf("associate: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[1] != 0x00 {
		t.Fatalf("associate reply: %v %v", err, reply)
	}
	return int(binary.BigEndian.Uint16(reply[8:10])), conn
}

func socksUDPHeaderIPv4(host string, port int) []byte {
	ip := net.ParseIP(host).To4()
	if ip == nil {
		panic("bad ip")
	}
	hdr := make([]byte, 10)
	hdr[3] = common.AtypIPv4
	copy(hdr[4:8], ip)
	binary.BigEndian.PutUint16(hdr[8:10], uint16(port))
	return hdr
}

func TestWBTJoinerRestartsSmuxOnPeerEpoch(t *testing.T) {
	skipIntegration(t)
	_, joinerTun := newPipePair()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	joiner, err := NewJoiner(ctx, joinerTun, "", "", func(string, ...any) {}, nil)
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	t.Cleanup(func() { joiner.Close() })

	joiner.mu.Lock()
	sessBefore := joiner.smuxSess
	joiner.mu.Unlock()
	if sessBefore == nil {
		t.Fatal("expected smux session")
	}

	joinerTun.triggerPeerRestart()

	joiner.mu.Lock()
	sessAfter := joiner.smuxSess
	joiner.mu.Unlock()
	if sessBefore == sessAfter {
		t.Fatal("joiner smux session should be recreated after peer epoch restart")
	}
}
