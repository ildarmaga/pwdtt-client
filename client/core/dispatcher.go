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
	returnChBuf = 384
	// sendChBuf — глубина общей очереди отправки (WG work-stealing).
	sendChBuf = 1024
	// rawWorkerSendBuf — очередь на воркер в RAW sticky-режиме.
	// Не дропать под спидтестом: 128 мало, TCP схлопывается в 0.
	rawWorkerSendBuf = 1024
	flowAffTTL       = 3 * time.Minute
	flowAffMax       = 8192
)

type WorkerSlot struct {
	ID     int
	SendCh chan []byte // RAW sticky: личный канал; WG: nil → общий Dispatcher.SendCh
}

type Dispatcher struct {
	localConn  net.PacketConn
	clientAddr atomic.Pointer[net.Addr]
	mu         sync.Mutex
	workers    []*WorkerSlot
	// flowAff: 5-tuple → worker ID. Нельзя hash%len(workers) — при join/leave
	// тот же TCP уезжает на другой IP и соединение мёртво.
	flowAff map[uint64]int
	flowExp map[uint64]int64
	// SendCh — ОБЩАЯ очередь (WG). RAW sticky шлёт в WorkerSlot.SendCh.
	SendCh    chan []byte
	ReturnCh  chan []byte
	rawSticky bool
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stats     *Stats
}

func NewDispatcher(ctx context.Context, localConn net.PacketConn, stats *Stats, rawSticky bool) *Dispatcher {
	dctx, dcancel := context.WithCancel(ctx)
	d := &Dispatcher{
		localConn: localConn,
		SendCh:    make(chan []byte, sendChBuf),
		ReturnCh:  make(chan []byte, returnChBuf),
		rawSticky: rawSticky,
		flowAff:   make(map[uint64]int),
		flowExp:   make(map[uint64]int64),
		ctx:       dctx,
		cancel:    dcancel,
		stats:     stats,
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
	if d.rawSticky && w.SendCh == nil {
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
	// Сбрасываем affinity мёртвого воркера — новые пакеты выберут живого.
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

// flowHash — 5-tuple IPv4 (или src/dst если портов нет).
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
		h ^= uint32(binary.BigEndian.Uint16(pkt[ihl : ihl+2]))
		h ^= uint32(binary.BigEndian.Uint16(pkt[ihl+2:ihl+4])) << 16
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

	w := d.workers[flowHash(pkt)%uint32(n)]
	if len(d.flowAff) >= flowAffMax {
		// Грубый prune просроченных.
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
	// Блокирующая отправка: дроп ломает TCP (спидтест → 0). Backpressure в TUN.
	select {
	case ch <- pkt:
		atomic.AddInt64(&d.stats.TotalBytesUp, int64(len(pkt)))
	case <-d.ctx.Done():
		putPktBuf(pkt)
	}
}

// readLoop читает пакеты с локального UDP (TUN bridge) и кладёт в очередь(и).
//
// WG: общая SendCh (work-stealing) — WireGuard терпит reorder.
// RAW: sticky affinity по 5-tuple → worker ID (стабильно при churn воркеров).
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
