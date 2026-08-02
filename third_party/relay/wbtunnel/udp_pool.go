package wbtunnel

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/xtaci/smux"
)

const udpPoolIdleTTL = 45 * time.Second
const udpPoolMaxEntries = 128

type udpPooledStream struct {
	stream *smux.Stream
	udp    *udpStream
	mu     sync.Mutex
	last   time.Time
}

type udpPool struct {
	mu    sync.Mutex
	items map[string]*udpPooledStream
}

func newUDPPool() *udpPool {
	return &udpPool{items: make(map[string]*udpPooledStream)}
}

func udpPoolKey(host string, port int) string {
	return host + "\x00" + strconv.Itoa(port)
}

func (p *udpPool) clear() {
	p.mu.Lock()
	for _, e := range p.items {
		e.mu.Lock()
		if e.udp != nil {
			e.udp.Close()
			e.udp = nil
		}
		e.stream = nil
		e.mu.Unlock()
	}
	p.items = make(map[string]*udpPooledStream)
	p.mu.Unlock()
}

func (p *udpPool) evictIdle() {
	now := time.Now()
	p.mu.Lock()
	for k, e := range p.items {
		e.mu.Lock()
		if e.stream != nil && now.Sub(e.last) > udpPoolIdleTTL {
			if e.udp != nil {
				e.udp.Close()
				e.udp = nil
			}
			e.stream = nil
			delete(p.items, k)
		}
		e.mu.Unlock()
	}
	p.mu.Unlock()
}

// tunnelUDP is a synchronous one-shot helper (SOCKS relay path). Each call
// reuses a pooled smux stream when possible.
func (j *Joiner) tunnelUDP(host string, port int, payload []byte) ([]byte, error) {
	key := udpPoolKey(host, port)

	for attempt := 0; attempt < 2; attempt++ {
		j.udpPool.mu.Lock()
		if attempt > 0 {
			delete(j.udpPool.items, key)
		}
		entry, ok := j.udpPool.items[key]
		if !ok {
			if len(j.udpPool.items) >= udpPoolMaxEntries {
				j.udpPool.mu.Unlock()
				return nil, fmt.Errorf("udp pool full")
			}
			entry = &udpPooledStream{}
			j.udpPool.items[key] = entry
		}
		j.udpPool.mu.Unlock()

		entry.mu.Lock()
		resp, err := j.tunnelUDPOn(entry, host, port, payload)
		if err == nil {
			entry.last = time.Now()
			entry.mu.Unlock()
			return resp, nil
		}
		if entry.udp != nil {
			entry.udp.Close()
			entry.udp = nil
		}
		entry.stream = nil
		entry.mu.Unlock()
	}
	return nil, fmt.Errorf("udp tunnel %s:%d failed", host, port)
}

func (j *Joiner) tunnelUDPOn(entry *udpPooledStream, host string, port int, payload []byte) ([]byte, error) {
	j.mu.Lock()
	sess := j.smuxSess
	j.mu.Unlock()
	if sess == nil {
		return nil, netErrClosed()
	}

	if entry.stream == nil {
		stream, err := sess.OpenStream()
		if err != nil {
			return nil, err
		}
		entry.stream = stream
		entry.udp = newUDPStream(stream)
		if err := writeUDPRequest(stream, host, port, payload); err != nil {
			entry.udp.Close()
			entry.udp = nil
			entry.stream = nil
			return nil, err
		}
	} else if err := writeUDPDatagram(entry.stream, payload); err != nil {
		return nil, err
	}

	select {
	case resp := <-entry.udp.inbound:
		if resp == nil {
			return nil, netErrClosed()
		}
		return resp, nil
	case <-entry.udp.done:
		return nil, netErrClosed()
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("udp response timeout")
	}
}

func writeUDPDatagram(stream *smux.Stream, payload []byte) error {
	if len(payload) > 0xFFFF {
		return fmt.Errorf("udp payload too large: %d", len(payload))
	}
	_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := writeUDPResponse(stream, payload); err != nil {
		return err
	}
	_ = stream.SetWriteDeadline(time.Time{})
	return nil
}
