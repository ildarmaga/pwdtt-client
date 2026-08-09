package core

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
)

// RAW multipath framing: magic "RA" + seq + IPv4.
// Reorder ЖДЁТ дырки (не скип) — иначе TCP cwnd схлопывается сильнее sticky.
const (
	rawFrameMagic0 = 'R'
	rawFrameMagic1 = 'A'
	rawFrameHeader = 6 // magic(2)+seq(4)
	rawReorderMax  = 2048
	// TURN paths на реальном ПК имеют 170–370 ms RTT. 40 ms преждевременно
	// скипал пакеты с более медленного worker и создавал искусственную потерю.
	rawReorderStallTTL = 500 * time.Millisecond
)

func rawFrameEncode(seq uint32, ip []byte, dst []byte) []byte {
	need := rawFrameHeader + len(ip)
	if cap(dst) < need {
		dst = make([]byte, need)
	} else {
		dst = dst[:need]
	}
	dst[0] = rawFrameMagic0
	dst[1] = rawFrameMagic1
	binary.BigEndian.PutUint32(dst[2:6], seq)
	copy(dst[rawFrameHeader:], ip)
	return dst
}

func rawFrameDecode(pkt []byte) (seq uint32, ip []byte, ok bool) {
	if len(pkt) < rawFrameHeader+20 {
		return 0, nil, false
	}
	if pkt[0] != rawFrameMagic0 || pkt[1] != rawFrameMagic1 {
		return 0, nil, false
	}
	seq = binary.BigEndian.Uint32(pkt[2:6])
	ip = pkt[rawFrameHeader:]
	if ip[0]>>4 != 4 {
		return 0, nil, false
	}
	return seq, ip, true
}

func isRawFrame(pkt []byte) bool {
	return len(pkt) >= rawFrameHeader && pkt[0] == rawFrameMagic0 && pkt[1] == rawFrameMagic1
}

// rawReorder — in-order delivery. Дырки держатся до прихода пакета (как WG window).
type rawReorder struct {
	mu        sync.Mutex
	next      uint32
	inited    bool
	buf       map[uint32][]byte
	waitSince time.Time // когда next стал отсутствовать при непустом buf
}

func newRawReorder() *rawReorder {
	return &rawReorder{buf: make(map[uint32][]byte)}
}

func (r *rawReorder) Push(seq uint32, ip []byte) [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.inited {
		r.next = seq
		r.inited = true
	}
	// слишком старый / дубль
	if seq < r.next && r.next-seq < 0x80000000 {
		return nil
	}
	if _, exists := r.buf[seq]; exists {
		return nil
	}
	if len(r.buf) >= rawReorderMax {
		// переполнение — аварийно прыгаем к seq (лучше дропнуть хвост, чем deadlock)
		r.next = seq
		r.buf = make(map[uint32][]byte)
		r.waitSince = time.Time{}
	}
	r.buf[seq] = append([]byte(nil), ip...)
	return r.drainLocked(time.Now(), false)
}

// FlushExpired releases a stalled tail even when no newer packet arrives.
// Push-only timeout handling left DNS and short TCP exchanges stuck forever.
func (r *rawReorder) FlushExpired() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.drainLocked(time.Now(), true)
}

func (r *rawReorder) drainLocked(now time.Time, timerTick bool) [][]byte {
	var out [][]byte
	for {
		if p, ok := r.buf[r.next]; ok {
			out = append(out, p)
			delete(r.buf, r.next)
			r.next++
			r.waitSince = time.Time{}
			continue
		}
		if len(r.buf) == 0 {
			r.waitSince = time.Time{}
			break
		}
		// ждём дырку; аварийный skip только после stall TTL
		if r.waitSince.IsZero() {
			r.waitSince = now
			break
		}
		if !timerTick || now.Sub(r.waitSince) < rawReorderStallTTL {
			break
		}
		// skip hole → ближайший buffered seq
		var minSeq uint32
		first := true
		for s := range r.buf {
			if first || s < minSeq {
				minSeq = s
				first = false
			}
		}
		r.next = minSeq
		r.waitSince = time.Time{}
	}
	return out
}

// rawSeq — глобальный счётчик исходящих RAW-фреймов.
type rawSeq struct{ v atomic.Uint32 }

func (s *rawSeq) Next() uint32 { return s.v.Add(1) - 1 }
