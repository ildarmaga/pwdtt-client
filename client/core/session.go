package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cbeuw/connutil"
	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

const (
	sessionReadTimeout = 30 * time.Minute // Increased from 60s to 30min
	readBufSize        = 1600
	socketBufSize      = 625 * 1024
	keepaliveByte      = 0xFF // DTLS-level keepalive marker
	keepaliveInterval  = 10 * time.Second
	dtlsHandshakeWait  = 28 * time.Second // cap: не ждать 45s на мёртвом TURN relay
	// consentTimeout — consent-freshness (идея из WebRTC): если по DTLS-пути нет
	// НИКАКОЙ входящей активности (данные ИЛИ pong на keepalive) дольше этого срока,
	// путь считается «чёрной дырой» и воркер убивается → пересоздаётся на здоровом
	// relay (pickHealthyTurnURL). Без этого зомби-воркер жил бы до sessionReadTimeout.
	consentTimeout = 90 * time.Second
)

// obfsDirectConn carries authenticated RTP/WRAP datagrams through TURN
// without a redundant DTLS layer. RAW always has a password-derived WRAP
// key, so confidentiality and packet authentication remain enabled.
type obfsDirectConn struct {
	relay      net.PacketConn
	peer       net.Addr
	wrapKey    []byte
	cfg        *ObfsConfig
	writeState *ObfsState
}

func (c *obfsDirectConn) Read(b []byte) (int, error) {
	wire := make([]byte, len(b)+80)
	for {
		n, _, err := c.relay.ReadFrom(wire)
		if err != nil {
			return 0, err
		}
		if !obfsIsRTPPacket(wire[:n]) {
			continue
		}
		plainN, err := obfsUnwrapPacket(c.wrapKey, wire[:n], b)
		if err != nil {
			continue
		}
		return plainN, nil
	}
}

func (c *obfsDirectConn) Write(b []byte) (int, error) {
	wire, err := obfsWrapPacket(c.wrapKey, b, c.cfg, c.writeState)
	if err != nil {
		return 0, err
	}
	if _, err := c.relay.WriteTo(wire, c.peer); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *obfsDirectConn) Close() error                       { return nil }
func (c *obfsDirectConn) LocalAddr() net.Addr                { return c.relay.LocalAddr() }
func (c *obfsDirectConn) RemoteAddr() net.Addr               { return c.peer }
func (c *obfsDirectConn) SetDeadline(t time.Time) error      { return c.relay.SetDeadline(t) }
func (c *obfsDirectConn) SetReadDeadline(t time.Time) error  { return c.relay.SetReadDeadline(t) }
func (c *obfsDirectConn) SetWriteDeadline(t time.Time) error { return c.relay.SetWriteDeadline(t) }

// Handshake semaphore: limit concurrent DTLS handshakes (queue under load)
var handshakeSem = make(chan struct{}, 6)

// turnAllocateSem: limit parallel TURN Allocate across all workers (VK quota)
var turnAllocateSem = make(chan struct{}, 4)

// NullLoggerFactory подавляет логи pion
type NullLoggerFactory struct{}

func (n *NullLoggerFactory) NewLogger(_ string) logging.LeveledLogger { return &NullLogger{} }

type NullLogger struct{}

func (n *NullLogger) Trace(_ string)                    {}
func (n *NullLogger) Tracef(_ string, _ ...interface{}) {}
func (n *NullLogger) Debug(_ string)                    {}
func (n *NullLogger) Debugf(_ string, _ ...interface{}) {}
func (n *NullLogger) Info(_ string)                     {}
func (n *NullLogger) Infof(_ string, _ ...interface{})  {}
func (n *NullLogger) Warn(_ string)                     {}
func (n *NullLogger) Warnf(_ string, _ ...interface{})  {}
func (n *NullLogger) Error(_ string)                    {}
func (n *NullLogger) Errorf(_ string, _ ...interface{}) {}

// connectedUDPConn — обёртка для connected UDP socket → PacketConn
type connectedUDPConn struct{ *net.UDPConn }

func (c *connectedUDPConn) WriteTo(p []byte, _ net.Addr) (int, error) { return c.Write(p) }

const turnDialTimeout = 12 * time.Second

// dialTurnPacketConn открывает канал к TURN: TCP (STUNConn) или UDP.
// Как qWDTT 1.4: TCP к TURN, Allocate всё ещё UDP-relay к peer (DTLS).
func dialTurnPacketConn(turnAddr string, useTCP bool) (net.PacketConn, func(), error) {
	if useTCP {
		conn, err := net.DialTimeout("tcp", turnAddr, turnDialTimeout)
		if err != nil {
			return nil, nil, fmt.Errorf("TURN TCP: %w", err)
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(30 * time.Second)
			_ = tc.SetReadBuffer(socketBufSize)
			_ = tc.SetWriteBuffer(socketBufSize)
		}
		return turn.NewSTUNConn(conn), func() { _ = conn.Close() }, nil
	}
	resolved, err := net.ResolveUDPAddr("udp", turnAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("резолв TURN: %w", err)
	}
	c, err := net.DialUDP("udp", nil, resolved)
	if err != nil {
		return nil, nil, fmt.Errorf("TURN UDP: %w", err)
	}
	_ = c.SetReadBuffer(socketBufSize)
	_ = c.SetWriteBuffer(socketBufSize)
	return &connectedUDPConn{c}, func() { _ = c.Close() }, nil
}

func openTurnRelay(
	ctx context.Context,
	turnAddr string,
	preferTCP bool,
	creds *Credentials,
	addrFamily turn.RequestedAddressFamily,
) (*turn.Client, func(), net.PacketConn, string, error) {
	try := func(useTCP bool) (*turn.Client, func(), net.PacketConn, string, error) {
		label := "udp"
		if useTCP {
			label = "tcp"
		}
		pkt, closer, err := dialTurnPacketConn(turnAddr, useTCP)
		if err != nil {
			return nil, nil, nil, label, err
		}
		tc, err := turn.NewClient(&turn.ClientConfig{
			STUNServerAddr:         turnAddr,
			TURNServerAddr:         turnAddr,
			Conn:                   pkt,
			Username:               creds.User,
			Password:               creds.Pass,
			RequestedAddressFamily: addrFamily,
			LoggerFactory:          &NullLoggerFactory{},
		})
		if err != nil {
			closer()
			return nil, nil, nil, label, fmt.Errorf("TURN клиент: %w", err)
		}
		if err = tc.Listen(); err != nil {
			tc.Close()
			closer()
			return nil, nil, nil, label, fmt.Errorf("TURN Listen: %w", err)
		}
		if err := ctx.Err(); err != nil {
			tc.Close()
			closer()
			return nil, nil, nil, label, err
		}
		relay, err := tc.Allocate()
		if err != nil {
			tc.Close()
			closer()
			return nil, nil, nil, label, fmt.Errorf("TURN Allocate: %w", err)
		}
		return tc, closer, relay, label, nil
	}

	if preferTCP {
		tc, closer, relay, label, err := try(true)
		if err == nil {
			return tc, closer, relay, label, nil
		}
		log.Printf("[TURN] TCP не вышло (%v) — fallback UDP", err)
		return try(false)
	}
	return try(false)
}

func RunSession(
	ctx context.Context,
	tp *TurnParams,
	peer *net.UDPAddr,
	d *Dispatcher,
	localPort string,
	configGate *wgConfigGate,
	sessionID int,
	creds *Credentials,
	deviceID, password string,
	stats *Stats,
) (configDelivered bool, err error) {
	if len(creds.TurnURLs) == 0 {
		return false, fmt.Errorf("нет TURN URL в учетных данных")
	}
	// Уводим воркеры с «дохлых» VK-relay на более стабильные (см. relay_health.go).
	selectedURL := pickHealthyTurnURL(creds.TurnURLs, sessionID)
	relayHost := relayHostKey(selectedURL)
	sessStart := time.Now()
	var becameReady bool
	var readyAt time.Time
	defer func() {
		life := time.Since(sessStart).Seconds()
		// Здоровье хоста фиксируем ВСЕГДА, включая провал Allocate/DTLS до READY.
		// becameReady=false → хост получает штраф (sec=0, shortStreak++), и
		// pickHealthyTurnURL уводит воркеров с мёртвого VK-relay, который не
		// отвечает на Allocate («all retransmissions failed»).
		readyLife := time.Duration(0)
		if becameReady {
			readyLife = time.Since(readyAt)
		}
		recordRelaySession(selectedURL, readyLife, becameReady)
		switch {
		case err != nil:
			log.Printf("[RELAY] воркер #%d host=%s life=%.1fs err=%v", sessionID, relayHost, life, err)
		case becameReady:
			log.Printf("[RELAY] воркер #%d host=%s life=%.1fs завершён", sessionID, relayHost, life)
		default:
			log.Printf("[RELAY] воркер #%d host=%s life=%.1fs (не дошёл до READY)", sessionID, relayHost, life)
		}
	}()

	urlhost, urlport, err := net.SplitHostPort(selectedURL)
	if err != nil {
		return false, fmt.Errorf("разбор TURN URL %q: %w", selectedURL, err)
	}
	if tp.Host != "" {
		urlhost = tp.Host
	}
	if tp.Port != "" {
		urlport = tp.Port
	}
	turnAddr := net.JoinHostPort(urlhost, urlport)

	preferTCP := tp.TurnTransport != "udp" // default tcp (как qWDTT 1.4)

	// RequestedAddressFamily
	var addrFamily turn.RequestedAddressFamily
	if peer.IP.To4() != nil {
		addrFamily = turn.RequestedAddressFamilyIPv4
	} else {
		addrFamily = turn.RequestedAddressFamilyIPv6
	}

	allocStart := time.Now()
	select {
	case turnAllocateSem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	tc, turnCloser, relay, transportLabel, err := openTurnRelay(ctx, turnAddr, preferTCP, creds, addrFamily)
	<-turnAllocateSem
	if err != nil {
		if isAuthError(err) {
			handleAuthError(creds.CacheStreamID)
		}
		errStr := err.Error()
		if strings.Contains(errStr, "Quota") || strings.Contains(errStr, "486") {
			return false, fmt.Errorf("TURN квота: %w", err)
		}
		return false, err
	}
	defer turnCloser()
	defer tc.Close()
	defer relay.Close()

	log.Printf("[СЕССИЯ #%d] TURN %s (%s)", sessionID, strings.ToUpper(transportLabel), turnAddr)

	atomic.StoreInt64(&stats.TurnRTTNs, time.Since(allocStart).Nanoseconds())

	// Reset error count on successful allocation
	getStreamCache(creds.CacheStreamID).errorCount.Store(0)

	log.Printf("[СЕССИЯ #%d] Relay: %s", sessionID, relay.LocalAddr())

	sessCtx, sessCancel := context.WithCancel(ctx)
	defer sessCancel()

	// Keepalive goroutine (TURN binding request)
	var sessionWg sync.WaitGroup
	sessionWg.Add(1)
	go func() {
		defer sessionWg.Done()
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-sessCtx.Done():
				return
			case <-t.C:
				tc.SendBindingRequest()
			}
		}
	}()

	useWrap := len(tp.WrapKey) == wrapKeyLen
	useDirectRaw := tp.TunnelMode == "raw" && useWrap
	var activeConn net.Conn
	var pipeA, pipeB *connutil.PacketPipe
	var relayWg sync.WaitGroup
	if useDirectRaw {
		obfsCfg := NewObfsConfig(tp.ObfsMode)
		obfsCfg.PaddingMax = 0
		activeConn = &obfsDirectConn{
			relay:      relay,
			peer:       peer,
			wrapKey:    tp.WrapKey,
			cfg:        obfsCfg,
			writeState: NewObfsState(),
		}
		log.Printf("[ВОРКЕР #%d] [DIRECT RAW] RTP/WRAP AEAD без DTLS ✓", sessionID)
	} else {
		// Legacy WG path: DTLS runs through an RTP/WRAP packet pipe.
		pipeA, pipeB = connutil.AsyncPacketPipe()
		relayWg.Add(2)

		var obfsCfg *ObfsConfig
		var obfsWriteState *ObfsState
		if useWrap {
			obfsCfg = NewObfsConfig(tp.ObfsMode)
			obfsWriteState = NewObfsState()
		}

		stopRelay := context.AfterFunc(sessCtx, func() {
			_ = relay.SetDeadline(time.Now())
			_ = pipeA.SetDeadline(time.Now())
		})
		defer stopRelay()

		// relay → pipeA (UNWRAP: strip RTP header + decrypt)
		go func() {
			defer relayWg.Done()
			defer sessCancel()
			// Max incoming: RTP header (12) + AEAD tag (16) + padding.
			// RTP(12)+tag(16)+pad≤61 — запас под video padding
			readBufLen := readBufSize + 100
			plainCap := readBufSize
			if tp.TunnelMode == "raw" {
				// RAW IP MTU≤1280 + DTLS record overhead; не резать datagram.
				readBufLen = 2048
				plainCap = 2048
			}
			buf := make([]byte, readBufLen)
			plain := make([]byte, plainCap)
			for {
				n, _, readErr := relay.ReadFrom(buf)
				if readErr != nil {
					return
				}
				payload := buf[:n]
				if useWrap {
					if !obfsIsRTPPacket(payload) {
						log.Printf("[СЕССИЯ #%d] OBFS unwrap: unexpected packet (n=%d)", sessionID, n)
						continue
					}
					m, wrapErr := obfsUnwrapPacket(tp.WrapKey, payload, plain)
					if wrapErr != nil {
						log.Printf("[СЕССИЯ #%d] OBFS unwrap: %v (n=%d)", sessionID, wrapErr, n)
						continue
					}
					payload = plain[:m]
				}
				if _, writeErr := pipeA.WriteTo(payload, peer); writeErr != nil {
					return
				}
			}
		}()

		// pipeA → relay (WRAP: add RTP header + encrypt)
		go func() {
			defer relayWg.Done()
			defer sessCancel()
			pipeBuf := readBufSize
			if tp.TunnelMode == "raw" {
				pipeBuf = 2048
			}
			b := make([]byte, pipeBuf)
			for {
				n, _, readErr := pipeA.ReadFrom(b)
				if readErr != nil {
					return
				}
				out := b[:n]
				if useWrap {
					if obfsCfg != nil && obfsWriteState != nil {
						wrapped, wrapErr := obfsWrapPacket(tp.WrapKey, out, obfsCfg, obfsWriteState)
						if wrapErr != nil {
							log.Printf("[СЕССИЯ #%d] OBFS wrap: %v", sessionID, wrapErr)
							return
						}
						out = wrapped
					}
				}
				if _, writeErr := relay.WriteTo(out, peer); writeErr != nil {
					return
				}
			}
		}()

		// DTLS с поддержкой Connection ID (без SNI)
		cert, err := selfsign.GenerateSelfSigned()
		if err != nil {
			return false, fmt.Errorf("генерация сертификата: %w", err)
		}

		// Acquire handshake semaphore
		select {
		case handshakeSem <- struct{}{}:
		case <-sessCtx.Done():
			return false, sessCtx.Err()
		}

		dtlsCfg := &dtls.Config{
			Certificates:          []tls.Certificate{cert},
			InsecureSkipVerify:    true,
			ExtendedMasterSecret:  dtls.RequireExtendedMasterSecret,
			CipherSuites:          []dtls.CipherSuiteID{dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
			ConnectionIDGenerator: dtls.OnlySendCIDGenerator(),
			FlightInterval:        100 * time.Millisecond,
			// No ServerName (SNI) — less detectable by DPI
		}

		dtlsConn, err := dtls.Client(pipeB, peer, dtlsCfg)
		if err != nil {
			<-handshakeSem
			return false, fmt.Errorf("DTLS клиент: %w", err)
		}
		defer dtlsConn.Close()

		hctx, hcancel := context.WithTimeout(sessCtx, dtlsHandshakeWait)
		log.Printf("[ВОРКЕР #%d] [DTLS] Рукопожатие (Handshake)...", sessionID)
		dtlsStart := time.Now()
		err = dtlsConn.HandshakeContext(hctx)
		hcancel()
		<-handshakeSem // RELEASE SEMAPHORE IMMEDIATELY AFTER HANDSHAKE

		if err != nil {
			if useWrap {
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "deadline") || strings.Contains(errStr, "timeout") {
					return false, fmt.Errorf("WRAP_AUTH_TIMEOUT: DTLS timeout (мёртвый TURN relay, повтор с новым allocation)")
				}
			}
			return false, fmt.Errorf("DTLS хендшейк: %w", err)
		}
		atomic.StoreInt64(&stats.DTLSHSNs, time.Since(dtlsStart).Nanoseconds())
		recordRelayPathRTT(turnAddr, float64(time.Since(allocStart).Milliseconds()))
		log.Printf("[ВОРКЕР #%d] [DTLS] Соединение установлено ✓", sessionID)
		activeConn = dtlsConn
	}

	atomic.AddInt32(&stats.ActiveConnections, 1)
	defer atomic.AddInt32(&stats.ActiveConnections, -1)

	// Запрос конфига (WG: GETCONF один раз; RAW: RAWCONF на каждый DTLS-conn → свой IP)
	var rawWorkerIP net.IP
	var rawPrimaryIP net.IP
	if configGate != nil && configGate.needsConfig() {
		delivered, workerIP, confErr := configGate.tryDeliver(sessionID, activeConn, localPort, deviceID, password)
		if confErr != nil {
			return false, confErr
		}
		if delivered {
			configDelivered = true
			rawWorkerIP = workerIP
		} else if configGate.tunnelMode == "raw" {
			return false, fmt.Errorf("RAW: пропуск воркера без RAWCONF")
		}
	}
	if configGate != nil && configGate.tunnelMode == "raw" {
		rawPrimaryIP = configGate.PrimaryIP()
		if rawWorkerIP == nil || rawPrimaryIP == nil {
			return false, fmt.Errorf("RAW: нет IP воркера/primary для rewrite")
		}
	}

	log.Printf("[ВОРКЕР #%d] [READY] Туннель готов к работе ✓", sessionID)
	becameReady = true
	readyAt = time.Now()

	// RAW multipath: общий SendCh (RR/work-steal) + RA-frame. Sticky — личный SendCh.
	slot := &WorkerSlot{ID: sessionID}
	slot.PathRTTMs.Store(time.Since(allocStart).Milliseconds())
	d.Register(slot)
	defer d.Unregister(slot)
	sendCh := d.SendCh
	if slot.SendCh != nil {
		sendCh = slot.SendCh
	}
	// Shared IP на сервере: все воркеры пишут с primary (не уникальный worker IP).
	rawSrcIP := rawPrimaryIP
	if rawSrcIP == nil {
		rawSrcIP = rawWorkerIP
	}

	// Proxy DTLS ↔ Dispatcher
	var proxyWg sync.WaitGroup
	proxyWg.Add(3) // +1 for keepalive goroutine

	stopDTLS := context.AfterFunc(sessCtx, func() {
		_ = activeConn.SetDeadline(time.Now())
	})
	defer stopDTLS()

	// lastInbound / lastOutbound — consent-freshness: путь мёртв только если
	// нет ответа И нет успешных отправок (upload-only воркеры без pong не убиваются).
	var lastInbound atomic.Int64
	var lastOutbound atomic.Int64
	now := time.Now().UnixNano()
	lastInbound.Store(now)
	lastOutbound.Store(now)
	var dtlsWriteMu sync.Mutex
	writeDTLS := func(payload []byte) (int, error) {
		if tp.TunnelMode != "raw" {
			return activeConn.Write(payload)
		}
		dtlsWriteMu.Lock()
		defer dtlsWriteMu.Unlock()
		return activeConn.Write(payload)
	}
	pingInterval := keepaliveInterval
	if tp.TunnelMode == "raw" {
		// Сервер исключает RAW allocation без uplink/keepalive через 25s.
		pingInterval = 3 * time.Second
	}

	// DTLS Keepalive + consent-freshness: шлём ping и проверяем, что путь жив.
	go func() {
		defer proxyWg.Done()
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		ping := []byte{keepaliveByte}
		for {
			select {
			case <-sessCtx.Done():
				return
			case <-t.C:
				inIdle := time.Since(time.Unix(0, lastInbound.Load()))
				outIdle := time.Since(time.Unix(0, lastOutbound.Load()))
				// Чёрная дыра: ни ответа, ни успешной отправки — убиваем воркер.
				if inIdle > consentTimeout && outIdle > consentTimeout {
					log.Printf("[ВОРКЕР #%d] [CONSENT] нет ответа %.0fs relay=%s — путь мёртв, пересоздание",
						sessionID, inIdle.Seconds(), relayHost)
					sessCancel()
					return
				}
				// Без WriteDeadline: абсолютный дедлайн протекал на Writer и
				// убивал воркер ровно через ~15s (ticker 10s + deadline 5s).
				if slot.PrioCh != nil {
					queuedPing := getPktBuf(len(ping))
					copy(queuedPing, ping)
					select {
					case slot.PrioCh <- queuedPing:
					default:
						putPktBuf(queuedPing)
					}
					continue
				}
				_, err := writeDTLS(ping)
				if err != nil {
					sessCancel()
					return
				}
				lastOutbound.Store(time.Now().UnixNano())
			}
		}
	}()

	// Writer: очередь → DTLS. Без per-packet WriteDeadline (hot path).
	go func() {
		defer proxyWg.Done()
		defer sessCancel()
		for {
			var pkt []byte
			var ok bool
			if slot.PrioCh != nil {
				select {
				case pkt, ok = <-slot.PrioCh:
				default:
					select {
					case <-sessCtx.Done():
						return
					case pkt, ok = <-slot.PrioCh:
					case pkt, ok = <-sendCh:
					}
				}
			} else {
				select {
				case <-sessCtx.Done():
					return
				case pkt, ok = <-sendCh:
				}
			}
			if !ok {
				return
			}
			if rawSrcIP != nil {
				_ = rewriteIPv4SrcInPlace(pkt, rawSrcIP)
			}
			out := pkt
			if d.rawMP && d.rawSeq != nil && len(pkt) >= 20 && pkt[0]>>4 == 4 {
				seq := d.rawSeq.Next()
				framed := rawFrameEncode(seq, pkt, nil)
				putPktBuf(pkt)
				out = framed
				pkt = nil
			}
			_, writeErr := writeDTLS(out)
			if pkt != nil {
				putPktBuf(pkt)
			}
			if writeErr != nil {
				log.Printf("[ВОРКЕР #%d] Ошибка Writer relay=%s: %v", sessionID, relayHost, writeErr)
				return
			}
			lastOutbound.Store(time.Now().UnixNano())
		}
	}()

	// Reader: DTLS → dispatcher
	go func() {
		defer proxyWg.Done()
		defer sessCancel()
		for {
			pkt := getPktBuf(2048)
			_ = activeConn.SetReadDeadline(time.Now().Add(sessionReadTimeout))
			n, readErr := activeConn.Read(pkt)
			if readErr != nil {
				putPktBuf(pkt)
				if sessCtx.Err() != nil {
					return
				}
				if ne, ok := readErr.(net.Error); ok && ne.Timeout() {
					continue
				}
				// Чистый EOF после READY = VK штатно рециклит TURN-allocation
				// (call-креды живут ~16 c). Это не сбой: воркер тут же
				// переподключится на новый relay. Логируем спокойным INFO
				// (без слова «Ошибка», чтобы не красить в ERROR).
				if isRoutineRelayClose(readErr) {
					log.Printf("[ВОРКЕР #%d] relay=%s переподключение (VK обновил relay)", sessionID, relayHost)
					return
				}
				log.Printf("[ВОРКЕР #%d] Ошибка Reader relay=%s: %v", sessionID, relayHost, readErr)
				return
			}

			// Любой входящий пакет (данные ИЛИ pong) = путь жив → consent-freshness.
			lastInbound.Store(time.Now().UnixNano())

			// Skip keepalive pong from server
			if n == 1 && pkt[0] == keepaliveByte {
				putPktBuf(pkt)
				continue
			}

			pkt = pkt[:n]
			if d.rawMP && d.rawReord != nil && isRawFrame(pkt) {
				seq, ip, ok := rawFrameDecode(pkt)
				if !ok {
					putPktBuf(pkt)
					continue
				}
				if rawPrimaryIP != nil {
					_ = rewriteIPv4DstInPlace(ip, rawPrimaryIP)
				}
				if !d.enqueueRawFrame(seq, ip) {
					putPktBuf(pkt)
					return
				}
				putPktBuf(pkt)
				continue
			}
			if rawPrimaryIP != nil {
				_ = rewriteIPv4DstInPlace(pkt, rawPrimaryIP)
			}
			if d.rawChunked {
				select {
				case d.ReturnCh <- pkt:
				default:
					putPktBuf(pkt)
				}
				continue
			}
			select {
			case d.ReturnCh <- pkt:
			case <-sessCtx.Done():
				putPktBuf(pkt)
				return
			}
		}
	}()

	proxyWg.Wait()
	sessCancel()
	relayWg.Wait()
	sessionWg.Wait()
	if pipeA != nil {
		_ = pipeA.Close()
	}
	if pipeB != nil {
		_ = pipeB.Close()
	}
	return configDelivered, nil
}

// isRoutineRelayClose — штатное закрытие relay удалённой стороной (VK рециклит
// TURN-allocation). Не сбой: воркер переподключается на свежий relay.
func isRoutineRelayClose(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "eof") || strings.Contains(low, "use of closed")
}
