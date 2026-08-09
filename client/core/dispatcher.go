package core

import (
	"context"
	"encoding/binary"
	"log"
	"net"
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
	// rawReturnChBuf — глубже downlink queue для RAW (UDP TURN иначе дропает при backpressure).
	rawReturnChBuf      = 4096
	rawChunkReturnChBuf = 512
	// sendChBuf — глубина общей очереди отправки (WG work-stealing).
	sendChBuf = 1024
	// rawWorkerSendBuf — очередь на воркер в RAW sticky (глубже = меньше drop ACK).
	rawWorkerSendBuf      = 8192
	rawChunkWorkerSendBuf = 128
	rawPrioBuf            = 32
	rawPrioThreshold      = 128
	rawChunkMaxDwell      = 15 * time.Millisecond
	flowAffTTL            = 3 * time.Minute
	flowAffMax            = 8192
)

type WorkerSlot struct {
	ID        int
	SendCh    chan []byte // RAW sticky: личный канал; WG/MP: nil → общий Dispatcher.SendCh
	PrioCh    chan []byte // RAW chunk: ACK/маленькие пакеты
	PathRTTMs atomic.Int64
}

type rawFramePacket struct {
	seq uint32
	ip  []byte
}

type Dispatcher struct {
	localConn  net.PacketConn
	clientAddr atomic.Pointer[net.Addr]
	mu         sync.Mutex
	workers    []*WorkerSlot
	// flowAff: legacy sticky (выкл. при rawMP).
	flowAff    map[uint64]int
	flowExp    map[uint64]int64
	SendCh     chan []byte
	ReturnCh   chan []byte
	rawSticky  bool // legacy; при rawMP=false
	rawChunked bool // adaptive chunks + ACK priority, без RA framing
	rawMP      bool // RAW multipath + RA framing/reorder
	rawSeq     *rawSeq
	rawReord   *rawReorder
	rawFrameCh chan rawFramePacket
	nextPick   uint32 // round-robin для новых sticky flows
	rrIndex    int
	rrCount    int
	lastPktAt  time.Time
	chunkStart time.Time
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	stats      *Stats
}

func NewDispatcher(ctx context.Context, localConn net.PacketConn, stats *Stats, rawMode, rawMultipath, rawChunked bool) *Dispatcher {
	dctx, dcancel := context.WithCancel(ctx)
	useMP := rawMode && rawMultipath
	useChunked := rawMode && rawChunked && !useMP
	retBuf := returnChBuf
	if rawMode {
		retBuf = rawReturnChBuf
	}
	if useChunked {
		retBuf = rawChunkReturnChBuf
	}
	d := &Dispatcher{
		localConn:  localConn,
		SendCh:     make(chan []byte, sendChBuf),
		ReturnCh:   make(chan []byte, retBuf),
		rawSticky:  rawMode && !useMP && !useChunked,
		rawChunked: useChunked,
		rawMP:      useMP,
		flowAff:    make(map[uint64]int),
		flowExp:    make(map[uint64]int64),
		ctx:        dctx,
		cancel:     dcancel,
		stats:      stats,
	}
	if d.rawMP {
		d.rawSeq = &rawSeq{}
		d.rawReord = newRawReorder()
		d.rawFrameCh = make(chan rawFramePacket, rawReturnChBuf)
		log.Printf("[ДИСП] RAW multipath (RA-frame reorder)")
	} else if d.rawChunked {
		log.Printf("[ДИСП] RAW adaptive chunks + ACK priority")
	} else if d.rawSticky {
		log.Printf("[ДИСП] RAW sticky")
	}

	d.wg.Add(2)
	go d.readLoop()
	go d.writeLoop()
	if d.rawMP {
		d.wg.Add(1)
		go d.rawFrameLoop()
	}
	return d
}

func (d *Dispatcher) Shutdown() {
	d.cancel()
	d.wg.Wait()
}

func (d *Dispatcher) Register(w *WorkerSlot) {
	d.mu.Lock()
	if (d.rawSticky || d.rawChunked) && !d.rawMP && w.SendCh == nil {
		size := rawWorkerSendBuf
		if d.rawChunked {
			size = rawChunkWorkerSendBuf
		}
		w.SendCh = make(chan []byte, size)
	}
	if d.rawChunked && w.PrioCh == nil {
		w.PrioCh = make(chan []byte, rawPrioBuf)
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

	w := d.pickAvailableWorkerLocked(0)
	if len(d.flowAff) >= flowAffMax {
		for k, exp := range d.flowExp {
			if now > exp {
				delete(d.flowAff, k)
				delete(d.flowExp, k)
			}
		}
		if len(d.flowAff) >= flowAffMax {
			for k := range d.flowAff {
				delete(d.flowAff, k)
				delete(d.flowExp, k)
				break
			}
		}
	}
	d.flowAff[key] = w.ID
	d.flowExp[key] = now + int64(flowAffTTL)
	return w
}

func (d *Dispatcher) pickAvailableWorkerLocked(excludeID int) *WorkerSlot {
	n := len(d.workers)
	if n == 0 {
		return nil
	}
	start := int(d.nextPick % uint32(n))
	d.nextPick++
	var best *WorkerSlot
	bestQueue := int(^uint(0) >> 1)
	for offset := 0; offset < n; offset++ {
		w := d.workers[(start+offset)%n]
		if w == nil || w.ID == excludeID || w.SendCh == nil {
			continue
		}
		if queued := len(w.SendCh); queued < bestQueue {
			best, bestQueue = w, queued
		}
	}
	return best
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
	// Не блокируем readLoop/ACK-clock: при полном SendCh дропаем пакет.
	// Иначе DTLS Write backpressure стопорит TUN→uplink и схлопывает TCP cwnd (~0.03 Mbit).
	select {
	case ch <- pkt:
		atomic.AddInt64(&d.stats.TotalBytesUp, int64(len(pkt)))
	case <-d.ctx.Done():
		putPktBuf(pkt)
	default:
		// Established sticky flow нельзя мигрировать без sequence framing:
		// старые пакеты ещё могут находиться в очереди исходного path.
		putPktBuf(pkt)
		if d.stats != nil {
			atomic.AddInt64(&d.stats.DroppedUp, 1)
		}
	}
}

func rawChunkSizeFor(pktSize int) int {
	switch {
	case pktSize > 1100:
		return 64
	case pktSize >= 701:
		return 24
	case pktSize >= 301:
		return 8
	case pktSize >= 101:
		return 3
	default:
		return 1
	}
}

func (d *Dispatcher) dispatchChunked(pkt []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.workers)
	if n == 0 {
		putPktBuf(pkt)
		return
	}
	now := time.Now()
	if !d.lastPktAt.IsZero() && now.Sub(d.lastPktAt) > 10*time.Millisecond {
		d.rrIndex = (d.rrIndex + 1) % n
		d.rrCount = 0
		d.chunkStart = now
	}
	d.lastPktAt = now

	if len(pkt) <= rawPrioThreshold {
		start := d.rrIndex % n
		for i := 0; i < n; i++ {
			w := d.workers[(start+i)%n]
			if w == nil || w.PrioCh == nil {
				continue
			}
			select {
			case w.PrioCh <- pkt:
				atomic.AddInt64(&d.stats.TotalBytesUp, int64(len(pkt)))
				return
			default:
			}
		}
	}

	if d.chunkStart.IsZero() {
		d.chunkStart = now
	} else if now.Sub(d.chunkStart) >= rawChunkMaxDwell {
		d.rrIndex = (d.rrIndex + 1) % n
		d.rrCount = 0
		d.chunkStart = now
	}
	chunk := rawChunkSizeFor(len(pkt))
	start := d.rrIndex % n
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		w := d.workers[idx]
		if w == nil || w.SendCh == nil {
			continue
		}
		select {
		case w.SendCh <- pkt:
			d.rrIndex = idx
			d.rrCount++
			if d.rrCount >= chunk {
				d.rrIndex = (idx + 1) % n
				d.rrCount = 0
				d.chunkStart = now
			}
			atomic.AddInt64(&d.stats.TotalBytesUp, int64(len(pkt)))
			return
		default:
		}
	}
	d.rrIndex = (start + 1) % n
	d.rrCount = 0
	putPktBuf(pkt)
	if d.stats != nil {
		atomic.AddInt64(&d.stats.DroppedUp, 1)
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

		d.clientAddr.Store(&addr)

		pkt := getPktBuf(n)
		copy(pkt, buf[:n])

		if d.rawChunked {
			d.dispatchChunked(pkt)
			continue
		}
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

func (d *Dispatcher) enqueueRawFrame(seq uint32, ip []byte) bool {
	pkt := append([]byte(nil), ip...)
	select {
	case d.rawFrameCh <- rawFramePacket{seq: seq, ip: pkt}:
		return true
	case <-d.ctx.Done():
		return false
	}
}

func (d *Dispatcher) rawFrameLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deliver := func(pkts [][]byte) bool {
		for _, pkt := range pkts {
			select {
			case d.ReturnCh <- pkt:
			case <-d.ctx.Done():
				return false
			}
		}
		return true
	}
	for {
		select {
		case <-d.ctx.Done():
			return
		case frame := <-d.rawFrameCh:
			if !deliver(d.rawReord.Push(frame.seq, frame.ip)) {
				return
			}
		case <-ticker.C:
			if !deliver(d.rawReord.FlushExpired()) {
				return
			}
		}
	}
}
