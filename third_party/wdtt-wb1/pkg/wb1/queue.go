package wb1

import "sync"

const (
	carrierDataCap = 1024
	carrierCtrlCap = 32
	ctrlFairLimit  = 4 // after this many ctrl pops, drain one data if queued
)

func isControlFrame(b []byte) bool {
	typ, _, _, ok := PeekRoute(b)
	return ok && (typ == TypeAck || typ == TypePing || typ == TypePong)
}

// packetQueue is a bounded drop-newest carrier queue with a small control lane.
// ACK/Ping/Pong bypass data; ctrl cap bounds flood; fair drain avoids starving data.
type packetQueue struct {
	mu         sync.Mutex
	data       [][]byte
	ctrl       [][]byte
	dataCap    int
	ctrlCap    int
	ctrlStreak int
	wait       chan struct{}
}

func newPacketQueue(dataCap, ctrlCap int) *packetQueue {
	if dataCap < 1 {
		dataCap = 1
	}
	if ctrlCap < 1 {
		ctrlCap = 1
	}
	return &packetQueue{
		dataCap: dataCap,
		ctrlCap: ctrlCap,
		wait:    make(chan struct{}, 1),
	}
}

func (q *packetQueue) Push(b []byte) bool {
	if q == nil || len(b) == 0 {
		return false
	}
	cp := append([]byte(nil), b...)
	q.mu.Lock()
	defer q.mu.Unlock()
	if isControlFrame(b) {
		typ, _, _, _ := PeekRoute(b)
		if typ == TypeAck {
			for i, item := range q.ctrl {
				t, _, _, ok := PeekRoute(item)
				if ok && t == TypeAck {
					q.ctrl[i] = cp
					q.signalLocked()
					return true
				}
			}
		}
		if len(q.ctrl) >= q.ctrlCap {
			if typ == TypeAck {
				q.ctrl = append(q.ctrl[1:], cp)
				q.signalLocked()
				return true
			}
			return false
		}
		q.ctrl = append(q.ctrl, cp)
		q.signalLocked()
		return true
	}
	if len(q.data) >= q.dataCap {
		return false
	}
	q.data = append(q.data, cp)
	q.signalLocked()
	return true
}

func (q *packetQueue) Pop() ([]byte, bool) {
	if q == nil {
		return nil, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	preferCtrl := len(q.ctrl) > 0 && (len(q.data) == 0 || q.ctrlStreak < ctrlFairLimit)
	if preferCtrl {
		b := q.ctrl[0]
		q.ctrl = q.ctrl[1:]
		q.ctrlStreak++
		return b, true
	}
	if len(q.data) > 0 {
		b := q.data[0]
		q.data = q.data[1:]
		q.ctrlStreak = 0
		return b, true
	}
	if len(q.ctrl) > 0 {
		b := q.ctrl[0]
		q.ctrl = q.ctrl[1:]
		return b, true
	}
	return nil, false
}

func (q *packetQueue) DataLen() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.data)
}

func (q *packetQueue) signalLocked() {
	select {
	case q.wait <- struct{}{}:
	default:
	}
}

func (q *packetQueue) Wait() <-chan struct{} {
	if q == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return q.wait
}
