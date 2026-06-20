package core

import (
	"context"
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
	// sendChBuf — глубина общей очереди отправки. Все воркеры читают из неё;
	// при кратковременной просадке числа живых воркеров (рециклинг VK-relay)
	// пакеты копятся здесь, а не дропаются, пока буфер не переполнен.
	sendChBuf = 1024
)

type WorkerSlot struct {
	ID int
}

type Dispatcher struct {
	localConn  net.PacketConn
	clientAddr atomic.Pointer[net.Addr]
	mu         sync.Mutex
	workers    []*WorkerSlot
	// SendCh — ОБЩАЯ очередь отправки (app→туннель). Все воркеры читают из неё
	// (work-stealing): кто свободен, тот забирает следующий пакет. Смерть одного
	// воркера не теряет «полосу» — остальные продолжают качать из той же очереди,
	// поэтому туннель не прерывается при рециклинге VK-relay. Модель эталонного
	// vk-turn-proxy (один sendCh на все conn'ы) вместо прежнего пуша пачками в
	// канал конкретного воркера (где смерть воркера = дроп его пакетов).
	SendCh   chan []byte
	ReturnCh chan []byte
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stats    *Stats
}

func NewDispatcher(ctx context.Context, localConn net.PacketConn, stats *Stats) *Dispatcher {
	dctx, dcancel := context.WithCancel(ctx)
	d := &Dispatcher{
		localConn: localConn,
		SendCh:    make(chan []byte, sendChBuf),
		ReturnCh:  make(chan []byte, returnChBuf),
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
	remaining := len(d.workers)
	d.mu.Unlock()
	log.Printf("[ДИСП] Воркер #%d отключён (осталось: %d)", slot.ID, remaining)
}

// readLoop читает WireGuard-пакеты и кладёт их в ОБЩУЮ очередь SendCh.
//
// Все воркеры конкурентно читают из этой очереди (work-stealing): свободный
// воркер забирает следующий пакет. Смерть одного воркера (рециклинг VK-relay)
// не теряет «полосу» — остальные продолжают качать из той же очереди, поэтому
// туннель не прерывается. Это модель эталонного vk-turn-proxy (один sendCh на
// все conn'ы) вместо прежнего пуша пачками в канал конкретного воркера.
//
// Порядок пакетов между воркерами не гарантируется, но WireGuard устойчив к
// переупорядочиванию (anti-replay window), поэтому это допустимо.
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

		select {
		case d.SendCh <- pkt:
			atomic.AddInt64(&d.stats.TotalBytesUp, int64(n))
		case <-d.ctx.Done():
			putPktBuf(pkt)
			return
		default:
			// Очередь переполнена (все воркеры залипли/мертвы) — дроп.
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
