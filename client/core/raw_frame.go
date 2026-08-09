package core

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
)

// RAW multipath framing: magic "RA" + seq + IPv4.
// Без reorder sticky=1 TURN; с framing можно RR как WG.
const (
	rawFrameMagic0   = 'R'
	rawFrameMagic1   = 'A'
	rawFrameHeader   = 6 // magic(2)+seq(4)
	rawReorderMax    = 512
	rawReorderGapTTL = 80 * time.Millisecond
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

// rawReorder собирает RR-потоки в порядок seq перед записью в TUN.
type rawReorder struct {
	mu      sync.Mutex
	next    uint32
	inited  bool
	buf     map[uint32][]byte
	deadline map[uint32]time.Time
}

func newRawReorder() *rawReorder {
	return &rawReorder{buf: make(map[uint32][]byte), deadline: make(map[uint32]time.Time)}
}

// Push возвращает 0..N пакетов в порядке для записи в TUN.
func (r *rawReorder) Push(seq uint32, ip []byte) [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.inited {
		r.next = seq
		r.inited = true
	}
	// слишком старый
	if seq < r.next && r.next-seq < 0x80000000 {
		return nil
	}
	if len(r.buf) >= rawReorderMax {
		// аварийный сдвиг окна
		r.flushExpiredLocked(time.Now())
		if len(r.buf) >= rawReorderMax {
			r.next = seq
			r.buf = make(map[uint32][]byte)
			r.deadline = make(map[uint32]time.Time)
		}
	}
	cp := append([]byte(nil), ip...)
	r.buf[seq] = cp
	r.deadline[seq] = time.Now().Add(rawReorderGapTTL)

	var out [][]byte
	now := time.Now()
	for {
		if p, ok := r.buf[r.next]; ok {
			out = append(out, p)
			delete(r.buf, r.next)
			delete(r.deadline, r.next)
			r.next++
			continue
		}
		// gap timeout → skip
		if r.flushExpiredLocked(now) {
			continue
		}
		break
	}
	return out
}

func (r *rawReorder) flushExpiredLocked(now time.Time) bool {
	if _, ok := r.buf[r.next]; ok {
		return false
	}
	// если есть пакеты с большим seq и next просрочен — скип
	if len(r.buf) == 0 {
		return false
	}
	// next считается просроченным если любой buffered пакет ждёт дольше TTL
	// и next отсутствует
	for s, dl := range r.deadline {
		if s >= r.next && now.After(dl) {
			// skip holes up to earliest ready
			for r.next < s {
				delete(r.buf, r.next)
				delete(r.deadline, r.next)
				r.next++
			}
			return true
		}
	}
	return false
}

// rawSeq — глобальный счётчик исходящих RAW-фреймов (на Core/device).
type rawSeq struct{ v atomic.Uint32 }

func (s *rawSeq) Next() uint32 { return s.v.Add(1) - 1 }
