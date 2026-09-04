package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const defaultMaxBytes = int64(2 << 30)

type report struct {
	Protocol          string  `json:"protocol"`
	Proxy             string  `json:"proxy"`
	Target            string  `json:"target"`
	StartedAt         string  `json:"started_at"`
	DurationSeconds   float64 `json:"duration_seconds"`
	PingMedianMS      float64 `json:"ping_median_ms"`
	PingP95MS         float64 `json:"ping_p95_ms"`
	LoadedPingP95MS   float64 `json:"loaded_ping_p95_ms"`
	DownloadMbps      float64 `json:"download_mbps"`
	UploadMbps        float64 `json:"upload_mbps"`
	DownloadBytes     int64   `json:"download_bytes"`
	UploadBytes       int64   `json:"upload_bytes"`
	Connections       int     `json:"connections"`
	FailedConnections int     `json:"failed_connections"`
	Error             string  `json:"error,omitempty"`
}

func main() {
	mode := flag.String("mode", "run", "run or serve")
	listen := flag.String("listen", "127.0.0.1:18080", "server listen address")
	target := flag.String("target", "127.0.0.1:18080", "benchmark server address")
	proxyURL := flag.String("proxy", "direct", "direct or socks5://[user:pass@]host:port")
	protocol := flag.String("protocol", "unknown", "wdtt|raw|csqtt|wb")
	token := flag.String("token", os.Getenv("WDTT_BENCH_TOKEN"), "shared benchmark token")
	sizeMB := flag.Int64("mb", 64, "bytes per direction in MiB")
	streams := flag.Int("streams", 4, "parallel streams per direction")
	pings := flag.Int("pings", 12, "unloaded and loaded ping samples")
	timeout := flag.Duration("timeout", 3*time.Minute, "whole benchmark timeout")
	jsonPath := flag.String("json", "", "write JSON report to file")
	flag.Parse()

	if *mode == "serve" {
		if err := serve(*listen, *token); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *streams < 1 || *streams > 64 || *sizeMB < 1 || *sizeMB > 2048 || *pings < 1 || *pings > 1000 {
		log.Fatal("invalid -streams, -mb or -pings")
	}

	dial, safeProxy, err := makeDialer(*proxyURL)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r := runBench(ctx, dial, *target, *protocol, safeProxy, *token, *sizeMB<<20, *streams, *pings, *timeout)
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	if *jsonPath != "" {
		if err := os.WriteFile(*jsonPath, append(b, '\n'), 0600); err != nil {
			log.Fatal(err)
		}
	}
	if r.Error != "" {
		os.Exit(1)
	}
}

type dialFunc func(context.Context, string) (net.Conn, error)

func makeDialer(raw string) (dialFunc, string, error) {
	if raw == "" || strings.EqualFold(raw, "direct") {
		d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 15 * time.Second}
		return func(ctx context.Context, address string) (net.Conn, error) { return d.DialContext(ctx, "tcp", address) }, "direct", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "socks5" || u.Host == "" {
		return nil, "", fmt.Errorf("invalid SOCKS URL %q", raw)
	}
	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	base := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 15 * time.Second}
	safe := "socks5://" + u.Host
	return func(ctx context.Context, address string) (net.Conn, error) {
		conn, err := base.DialContext(ctx, "tcp", u.Host)
		if err != nil {
			return nil, err
		}
		if err = socksConnect(conn, address, username, password); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}, safe, nil
}

func socksConnect(conn net.Conn, target, username, password string) error {
	methods := []byte{0}
	if username != "" || password != "" {
		methods = []byte{2}
	}
	if _, err := conn.Write(append([]byte{5, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil {
		return err
	}
	if method[0] != 5 || method[1] == 0xff {
		return errors.New("SOCKS authentication method rejected")
	}
	if method[1] == 2 {
		if len(username) > 255 || len(password) > 255 {
			return errors.New("SOCKS credentials too long")
		}
		auth := []byte{1, byte(len(username))}
		auth = append(auth, username...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, password...)
		if _, err := conn.Write(auth); err != nil {
			return err
		}
		var reply [2]byte
		if _, err := io.ReadFull(conn, reply[:]); err != nil {
			return err
		}
		if reply[1] != 0 {
			return errors.New("SOCKS authentication rejected")
		}
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		return err
	}
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 1)
			req = append(req, v4...)
		} else {
			req = append(req, 4)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("SOCKS target name too long")
		}
		req = append(req, 3, byte(len(host)))
		req = append(req, host...)
	}
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	req = append(req, portBytes[:]...)
	if _, err = conn.Write(req); err != nil {
		return err
	}
	var reply [4]byte
	if _, err = io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[0] != 5 || reply[1] != 0 {
		return fmt.Errorf("SOCKS CONNECT rejected: code %d", reply[1])
	}
	var skip int
	switch reply[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		var n [1]byte
		if _, err = io.ReadFull(conn, n[:]); err != nil {
			return err
		}
		skip = int(n[0])
	default:
		return errors.New("invalid SOCKS reply address")
	}
	_, err = io.CopyN(io.Discard, conn, int64(skip+2))
	return err
}

func serve(address, token string) error {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Printf("tunnel-bench server listening on %s", ln.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handleConn(conn, token)
	}
}

func handleConn(conn net.Conn, token string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	var req request
	if err := readJSON(conn, &req); err != nil {
		return
	}
	if err := validateRequest(req, token, defaultMaxBytes); err != nil {
		_ = writeJSON(conn, response{Error: err.Error()})
		return
	}
	if err := writeJSON(conn, response{OK: true}); err != nil {
		return
	}
	switch req.Op {
	case opPing:
		return
	case opDownload:
		_, _ = io.CopyN(conn, zeroReader{}, req.Bytes)
	case opUpload:
		n, _ := io.CopyN(io.Discard, conn, req.Bytes)
		_ = writeJSON(conn, response{OK: n == req.Bytes, Bytes: n})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func runBench(parent context.Context, dial dialFunc, target, protocol, safeProxy, token string, bytes int64, streams, pingCount int, timeout time.Duration) report {
	started := time.Now()
	r := report{Protocol: strings.ToLower(protocol), Proxy: safeProxy, Target: target, StartedAt: started.UTC().Format(time.RFC3339), Connections: streams}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	plain, failures, err := measurePings(ctx, dial, target, token, pingCount)
	if err != nil {
		r.Error = err.Error()
		r.FailedConnections += failures
		return r
	}
	r.PingMedianMS = durationMS(percentile(plain, .50))
	r.PingP95MS = durationMS(percentile(plain, .95))

	downStart := time.Now()
	down, downFailures, err := transfer(ctx, dial, target, token, opDownload, bytes, streams)
	r.DownloadBytes, r.DownloadMbps = down, mbps(down, time.Since(downStart))
	r.FailedConnections += downFailures
	if err != nil {
		r.Error = err.Error()
		return r
	}

	upStart := time.Now()
	up, upFailures, err := transfer(ctx, dial, target, token, opUpload, bytes, streams)
	r.UploadBytes, r.UploadMbps = up, mbps(up, time.Since(upStart))
	r.FailedConnections += upFailures
	if err != nil {
		r.Error = err.Error()
		return r
	}

	loadCtx, loadCancel := context.WithCancel(ctx)
	loadDone := make(chan struct{})
	go func() { _, _, _ = transfer(loadCtx, dial, target, token, opDownload, bytes, streams); close(loadDone) }()
	loaded, loadedFailures, loadedErr := measurePings(ctx, dial, target, token, pingCount)
	loadCancel()
	<-loadDone
	r.FailedConnections += loadedFailures
	if loadedErr == nil {
		r.LoadedPingP95MS = durationMS(percentile(loaded, .95))
	}
	r.DurationSeconds = time.Since(started).Seconds()
	return r
}

func durationMS(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func measurePings(ctx context.Context, dial dialFunc, target, token string, count int) ([]time.Duration, int, error) {
	values := make([]time.Duration, 0, count)
	failures := 0
	for i := 0; i < count; i++ {
		start := time.Now()
		conn, err := dial(ctx, target)
		if err == nil {
			err = writeJSON(conn, request{Token: token, Op: opPing})
			var resp response
			if err == nil {
				err = readJSON(conn, &resp)
				if err == nil && !resp.OK {
					err = errors.New(resp.Error)
				}
			}
			_ = conn.Close()
		}
		if err != nil {
			failures++
			continue
		}
		values = append(values, time.Since(start))
		time.Sleep(50 * time.Millisecond)
	}
	if len(values) == 0 {
		return nil, failures, errors.New("all ping samples failed")
	}
	return values, failures, nil
}

func transfer(ctx context.Context, dial dialFunc, target, token, op string, totalBytes int64, streams int) (int64, int, error) {
	perStream := totalBytes / int64(streams)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var transferred int64
	failures := 0
	var firstErr error
	for i := 0; i < streams; i++ {
		size := perStream
		if i == streams-1 {
			size += totalBytes - perStream*int64(streams)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := dial(ctx, target)
			if err == nil {
				err = writeJSON(conn, request{Token: token, Op: op, Bytes: size})
			}
			var resp response
			if err == nil {
				err = readJSON(conn, &resp)
				if err == nil && !resp.OK {
					err = errors.New(resp.Error)
				}
			}
			var n int64
			if err == nil && op == opDownload {
				n, err = io.CopyN(io.Discard, conn, size)
			}
			if err == nil && op == opUpload {
				n, err = io.CopyN(conn, zeroReader{}, size)
				if err == nil {
					err = readJSON(conn, &resp)
				}
			}
			if conn != nil {
				_ = conn.Close()
			}
			mu.Lock()
			defer mu.Unlock()
			transferred += n
			if err != nil {
				failures++
				if firstErr == nil {
					firstErr = err
				}
			}
		}()
	}
	wg.Wait()
	return transferred, failures, firstErr
}
