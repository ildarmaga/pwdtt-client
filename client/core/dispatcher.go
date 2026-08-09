package core

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var pktPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 2048)
	},
}

func getPktBuf(size int) []byte {
	b := pktPool.Get().([]byte)
	if cap(b) < size {
		b = make([]byte, size)
	}
	return b[:size]
}

func putPktBuf(b []byte) {
	if cap(b) < 2048 {
		return
	}
	pktPool.Put(b[:cap(b)])
}

const (
	returnChBuf = 1024
	// sendChBuf — глубина общей очереди отправки (WG / RAW multipath).
	sendChBuf = 4096
	// rawWorkerSendBuf — очередь на воркер в RAW sticky.
	rawWorkerSendBuf = 2048
	flowAffTTL       = 3 * time.Minute
	flowAffMax       = 8192
)

type WorkerSlot struct {
	ID        int
	SendCh    chan []byte // RAW sticky: личный канал; WG/MP: nil → общий Dispatcher.SendCh
	PathRTTMs atomic.Int64
}

type Dispatcher struct {
	localConn  net.PacketConn
	clientAddr atomic.Pointer[net.Addr]
	mu         sync.Mutex
	workers    []*WorkerSlot
	// flowAff: legacy sticky (выкл. при rawMP).
	flowAff map[uint64]int
	flowExp map[uint64]int64
	SendCh    chan []byte
	ReturnCh  chan []byte
	rawSticky bool // legacy; при rawMP=false
	rawMP     bool // RAW multipath + RA framing/reorder
	rawSeq    *rawSeq
	rawReord  *rawReorder
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stats     *Stats
}

// NewDispatcher: RAW по умолчанию sticky (TCP TURN). Multipath только RAW_MP=1.
// Не включаем MP от turnTransport=udp — на части сетей UDP TURN даёт 0 трафика.
func NewDispatcher(ctx context.Context, localConn net.PacketConn, stats *Stats, rawMode bool, turnTransport string) *Dispatcher {
	dctx, dcancel := context.WithCancel(ctx)
	_ = turnTransport
	forceSticky := strings.TrimSpace(os.Getenv("RAW_STICKY")) == "1"
	forceMP := strings.TrimSpace(os.Getenv("RAW_MP")) == "1"
	useMP := rawMode && forceMP && !forceSticky
	useSticky := rawMode && !useMP
	d := &Dispatcher{
		localConn: localConn,
		SendCh:    make(chan []byte, sendChBuf),
		ReturnCh:  make(chan []byte, returnChBuf*4),
		rawSticky: useSticky,
		rawMP:     useMP,
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
		ctx:       dctx,
		cancel:    dcancel,
		stats:     stats,
	}
	if d.rawMP {
		d.rawSeq = &rawSeq{}
		d.rawReord = newRawReorder()
		log.Printf("[ДИСП] RAW multipath (RA-frame, RAW_MP=1)")
	} else if d.rawSticky {
		log.Printf("[ДИСП] RAW sticky")
	}

	d.wg.Add(2)
	go d.readLoop()
	go d.writeLoop()
	return d
}

func (d *Dispatcher) Shutdown() {
	d.cancel()
	d.wg.Wait()
}

func (d *Dispatcher) Register(w *WorkerSlot) {
	d.mu.Lock()
	if d.rawSticky && !d.rawMP && w.SendCh == nil {
		w.SendCh = make(chan []byte, rawWorkerSendBuf)
	}
	d.workers = append(d.workers, w)
	count := len(d.workers)
	d.mu.Unlock()
	log.Printf("[ДИСП] Воркер #%d зарегистрирован (всего: %d)", w.ID, count)
}

func (d *Dispatcher) Unregister(slot *WorkerSlot) {
	d.mu.Lock()
	for i, w := range d.workers {
		if w == slot {
			d.workers = append(d.workers[:i], d.workers[i+1:]...)
			break
		}
	}
	for k, id := range d.flowAff {
		if id == slot.ID {
			delete(d.flowAff, k)
			delete(d.flowExp, k)
		}
	}
	remaining := len(d.workers)
	d.mu.Unlock()
	log.Printf("[ДИСП] Воркер #%d отключён (осталось: %d)", slot.ID, remaining)
}

func flowHash(pkt []byte) uint32 {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		var h uint32
		for i := 0; i < len(pkt) && i < 64; i++ {
			h = h*131 + uint32(pkt[i])
		}
		return h
	}
	ihl := int(pkt[0]&0x0f) * 4
	h := binary.BigEndian.Uint32(pkt[12:16]) ^ binary.BigEndian.Uint32(pkt[16:20])
	h ^= uint32(pkt[9]) * 0x9e3779b9
	if len(pkt) >= ihl+4 && (pkt[9] == 6 || pkt[9] == 17) {
		a := binary.BigEndian.Uint16(pkt[ihl : ihl+2])
		b := binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4])
		if a > b {
			a, b = b, a
		}
		// min|max — симметрично up/down; multiply размазывает биты (иначе %N=степень 2 схлопывается).
		h ^= (uint32(a)<<16 | uint32(b)) * 0x9e3779b9
	}
	return h
}

func flowKey(pkt []byte) uint64 {
	return uint64(flowHash(pkt))
}

func (d *Dispatcher) workerByIDLocked(id int) *WorkerSlot {
	for _, w := range d.workers {
		if w.ID == id {
			return w
		}
	}
	return nil
}

func (d *Dispatcher) pickStickyLocked(pkt []byte) *WorkerSlot {
	n := len(d.workers)
	if n == 0 {
		return nil
	}
	now := time.Now().UnixNano()
	key := flowKey(pkt)

	if id, ok := d.flowAff[key]; ok {
		if exp, ok2 := d.flowExp[key]; ok2 && now <= exp {
			if w := d.workerByIDLocked(id); w != nil {
				d.flowExp[key] = now + int64(flowAffTTL)
				return w
			}
		}
		delete(d.flowAff, key)
		delete(d.flowExp, key)
	}

	// Новый поток → воркер с мин. очередью и RTT (не слепой hash%N на плохой TURN).
	w := d.pickBestWorkerLocked()
	if w == nil {
		w = d.workers[flowHash(pkt)%uint32(n)]
	}
	if len(d.flowAff) >= flowAffMax {
		for k, exp := range d.flowExp {
			if now > exp {
				delete(d.flowAff, k)
				delete(d.flowExp, k)
			}
		}
	}
	d.flowAff[key] = w.ID
	d.flowExp[key] = now + int64(flowAffTTL)
	return w
}

func (d *Dispatcher) pickBestWorkerLocked() *WorkerSlot {
	var best *WorkerSlot
	bestScore := int64(1 << 62)
	for _, w := range d.workers {
		if w == nil {
			continue
		}
		q := int64(0)
		if w.SendCh != nil {
			q = int64(len(w.SendCh))
		}
		rtt := w.PathRTTMs.Load()
		if rtt <= 0 {
			rtt = 500
		}
		score := q*1000 + rtt
		if score < bestScore {
			bestScore = score
			best = w
		}
	}
	return best
}

func (d *Dispatcher) dispatchSticky(pkt []byte) {
	d.mu.Lock()
	w := d.pickStickyLocked(pkt)
	var ch chan []byte
	if w != nil {
		ch = w.SendCh
	}
	d.mu.Unlock()
	if ch == nil {
		putPktBuf(pkt)
		return
	}
	select {
	case ch <- pkt:
		atomic.AddInt64(&d.stats.TotalBytesUp, int64(len(pkt)))
	case <-d.ctx.Done():
		putPktBuf(pkt)
	}
}

// readLoop:
// WG — shared SendCh (work-steal, drop OK).
// RAW — sticky 5-tuple→worker при shared IP (без WG reorder window chunk вреден).
func (d *Dispatcher) readLoop() {
	defer d.wg.Done()

	buf := make([]byte, readBufSize)
	for {
		if err := d.ctx.Err(); err != nil {
			return
		}

		n, addr, err := d.localConn.ReadFrom(buf)
		if err != nil {
			if d.ctx.Err() != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}

		// Адрес TUN-bridge нужен для downlink WriteTo. Регистрируем даже
		// по probe (не IPv4), иначе server→client трафик молча дропается
		// пока клиент сам ничего не отправил в TUN.
		d.clientAddr.Store(&addr)
		if n < 20 || buf[0]>>4 != 4 {
			continue
		}

		pkt := getPktBuf(n)
		copy(pkt, buf[:n])

		if d.rawSticky {
			d.dispatchSticky(pkt)
			continue
		}

		select {
		case d.SendCh <- pkt:
			atomic.AddInt64(&d.stats.TotalBytesUp, int64(n))
		case <-d.ctx.Done():
			putPktBuf(pkt)
			return
		default:
			putPktBuf(pkt)
		}
	}
}

func (d *Dispatcher) writeLoop() {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		case pkt := <-d.ReturnCh:
			addrPtr := d.clientAddr.Load()
			if addrPtr == nil {
				putPktBuf(pkt)
				continue
			}
			addr := *addrPtr
			if _, err := d.localConn.WriteTo(pkt, addr); err != nil {
				if d.ctx.Err() != nil {
					putPktBuf(pkt)
					return
				}
			}
			atomic.AddInt64(&d.stats.TotalBytesDown, int64(len(pkt)))
			putPktBuf(pkt)
		}
	}
}
