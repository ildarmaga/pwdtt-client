package wb1

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	ackDelay             = 10 * time.Millisecond
	initialRTO           = 200 * time.Millisecond
	minRTO               = 120 * time.Millisecond
	maxRTO               = 3 * time.Second
	arqStallTimeout      = 10 * time.Second
	maxARQRetries        = 20
	retransmitTick       = 10 * time.Millisecond
	maxRetransmitPerTick = 64
	fastDupFloor         = 20 * time.Millisecond
)

type sendSlot struct {
	seq      uint32
	frame    Frame
	wire     []byte
	sentAt   time.Time
	firstAt  time.Time
	lastFast time.Time
	retries  int
	karn     bool
}

func (m *Mux) handleIncoming(ctx context.Context, f Frame) {
	if !m.acceptEpoch(f) {
		return
	}
	switch {
	case f.Type == TypeAck:
		m.handleAck(ctx, f)
	case isReliable(f.Type):
		m.recvReliable(ctx, f)
	default:
		m.dispatch(ctx, f)
	}
}

func (m *Mux) acceptEpoch(f Frame) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.epochSet {
		m.remoteEpoch = f.Epoch
		m.epochSet = true
		return true
	}
	return f.Epoch == m.remoteEpoch
}

func (m *Mux) recvReliable(ctx context.Context, f Frame) {
	m.mu.Lock()
	if f.Seq < m.recvNext {
		m.mu.Unlock()
		m.drainRecvBuf(ctx)
		m.ackImmediate(ctx)
		return
	}
	if f.Seq != m.recvNext {
		if f.Seq >= m.recvNext+uint32(ARQWindow) {
			m.mu.Unlock()
			return
		}
		if _, exists := m.recvBuf[f.Seq]; exists {
			m.mu.Unlock()
			m.ackImmediate(ctx)
			return
		}
		if len(m.recvBuf) >= ARQWindow {
			m.mu.Unlock()
			return
		}
		m.recvBuf[f.Seq] = f
		m.mu.Unlock()
		m.ackImmediate(ctx)
		return
	}
	if _, exists := m.recvBuf[f.Seq]; !exists {
		m.recvBuf[f.Seq] = f
	}
	m.mu.Unlock()
	m.drainRecvBuf(ctx)
}

func (m *Mux) drainRecvBuf(ctx context.Context) {
	m.drainMu.Lock()
	defer m.drainMu.Unlock()
	for {
		m.mu.Lock()
		cur, ok := m.recvBuf[m.recvNext]
		m.mu.Unlock()
		if !ok {
			return
		}
		if !m.admitReliable(ctx, cur) {
			return
		}
		m.mu.Lock()
		if m.recvNext != cur.Seq {
			m.mu.Unlock()
			return
		}
		delete(m.recvBuf, m.recvNext)
		m.recvNext++
		_, more := m.recvBuf[m.recvNext]
		gapped := len(m.recvBuf) > 0
		m.mu.Unlock()
		if !more {
			if gapped {
				m.ackImmediate(ctx)
			} else {
				m.ackDelayed(ctx)
			}
			return
		}
	}
}

func (m *Mux) wakeDrain() {
	if m == nil || m.closed.Load() {
		return
	}
	select {
	case m.drainCh <- struct{}{}:
	default:
	}
}

func (m *Mux) drainLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.closeCh:
			return
		case <-m.drainCh:
			if m.closed.Load() || ctx.Err() != nil {
				return
			}
			m.drainRecvBuf(ctx)
		}
	}
}

func (m *Mux) admitReliable(ctx context.Context, f Frame) bool {
	m.notePeer()
	switch f.Type {
	case TypeData:
		m.mu.Lock()
		sc := m.streams[f.StreamID]
		m.mu.Unlock()
		if sc == nil {
			return true
		}
		switch sc.push(f.Payload) {
		case pushAdmitted:
			m.addTraffic(0, int64(len(f.Payload)))
			return true
		case pushClosed:
			return true
		default:
			return false
		}
	case TypeHello:
		return true
	case TypeOpen:
		m.handleOpen(ctx, f)
		return true
	case TypeFin, TypeErr:
		m.handleFin(f)
		return true
	default:
		m.dispatch(ctx, f)
		return true
	}
}

func (m *Mux) ackImmediate(ctx context.Context) {
	m.stopDelayAck()
	m.sendAck(ctx)
}

func (m *Mux) ackDelayed(ctx context.Context) {
	m.mu.Lock()
	if m.delayAck != nil {
		m.mu.Unlock()
		return
	}
	runCtx := ctx
	m.delayAck = time.AfterFunc(ackDelay, func() {
		m.mu.Lock()
		m.delayAck = nil
		m.mu.Unlock()
		m.sendAck(runCtx)
	})
	m.mu.Unlock()
}

func (m *Mux) stopDelayAck() {
	m.mu.Lock()
	if m.delayAck != nil {
		m.delayAck.Stop()
		m.delayAck = nil
	}
	m.mu.Unlock()
}

func (m *Mux) sendAck(ctx context.Context) {
	if m.closed.Load() {
		return
	}
	m.mu.Lock()
	cum := m.recvNext
	var sack sackBitmap
	for i := uint32(0); i < uint32(sackWords)*64; i++ {
		if _, ok := m.recvBuf[cum+1+i]; ok {
			sack.set(i)
		}
	}
	wnd := ARQWindow - len(m.recvBuf)
	if wnd < 1 {
		wnd = 1
	}
	if wnd > ARQWindow {
		wnd = ARQWindow
	}
	epoch := m.epoch
	m.mu.Unlock()
	_ = m.sendUnsequenced(ctx, Frame{
		Type:    TypeAck,
		Epoch:   epoch,
		Payload: packAckBitmap(cum, sack, uint16(wnd)),
	})
}

func (m *Mux) handleAck(ctx context.Context, f Frame) {
	cum, sack, wnd, ok := unpackAckBitmap(f.Payload)
	if !ok {
		return
	}
	now := time.Now()
	m.mu.Lock()
	m.peerRecvWnd = clampPeerWnd(wnd)
	m.gotPeerWnd = true
	newly := 0
	if cum > m.sendUnacked && cum <= m.sendNext {
		for seq := m.sendUnacked; seq < cum; seq++ {
			if slot, ok := m.sendBuf[seq]; ok {
				if !slot.karn && slot.retries == 0 {
					m.updateRTOLocked(now.Sub(slot.sentAt))
				}
				delete(m.sendBuf, seq)
				newly++
			}
		}
		m.sendUnacked = cum
	}
	for i := uint32(0); i < uint32(sackWords)*64; i++ {
		if !sack.has(i) {
			continue
		}
		seq := cum + 1 + i
		slot, ok := m.sendBuf[seq]
		if !ok {
			continue
		}
		if !slot.karn && slot.retries == 0 {
			m.updateRTOLocked(now.Sub(slot.sentAt))
		}
		delete(m.sendBuf, seq)
		newly++
	}
	if newly > 0 {
		m.lastProgress = now
		m.inRecovery = false
		m.cwnd += newly
		if m.cwnd > ARQWindow {
			m.cwnd = ARQWindow
		}
	}
	fast := m.collectFastRetransmitLocked(now, cum, sack)
	m.mu.Unlock()
	for _, slot := range fast {
		if m.closed.Load() || ctx.Err() != nil {
			break
		}
		_ = m.carrier.Send(ctx, slot.wire)
	}
	m.wakeSend()
}

func (m *Mux) collectFastRetransmitLocked(now time.Time, cum uint32, sack sackBitmap) []*sendSlot {
	highest := -1
	for i := int(sackWords)*64 - 1; i >= 0; i-- {
		if sack.has(uint32(i)) {
			highest = i
			break
		}
	}
	if highest < 0 {
		return nil
	}
	highestSeq := cum + 1 + uint32(highest)
	gap := fastDupFloor
	type cand struct {
		seq  uint32
		slot *sendSlot
	}
	var holes []cand
	for seq, slot := range m.sendBuf {
		if seq > highestSeq {
			continue
		}
		if len(slot.wire) == 0 {
			continue
		}
		if !slot.lastFast.IsZero() && now.Sub(slot.lastFast) < gap {
			continue
		}
		holes = append(holes, cand{seq: seq, slot: slot})
	}
	sort.Slice(holes, func(i, j int) bool { return holes[i].seq < holes[j].seq })
	if len(holes) > maxRetransmitPerTick {
		holes = holes[:maxRetransmitPerTick]
	}
	out := make([]*sendSlot, 0, len(holes))
	for _, h := range holes {
		h.slot.lastFast = now
		h.slot.karn = true
		out = append(out, h.slot)
	}
	return out
}

func (m *Mux) updateRTOLocked(sample time.Duration) {
	if sample <= 0 {
		return
	}
	if m.srtt == 0 {
		m.srtt = sample
		m.rttvar = sample / 2
	} else {
		diff := m.srtt - sample
		if diff < 0 {
			diff = -diff
		}
		m.rttvar = (3*m.rttvar + diff) / 4
		m.srtt = (7*m.srtt + sample) / 8
	}
	rto := m.srtt + 4*m.rttvar
	if rto < minRTO {
		rto = minRTO
	}
	if rto > maxRTO {
		rto = maxRTO
	}
	m.rto = rto
}

func stopTimer(t *time.Timer) {
	if t == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func clampPeerWnd(wnd uint16) uint16 {
	if wnd < 1 {
		return 1
	}
	if int(wnd) > ARQWindow {
		return uint16(ARQWindow)
	}
	return wnd
}

func (m *Mux) sendLimitLocked() int {
	if !m.gotPeerWnd {
		return initialFlight
	}
	limit := m.cwnd
	if limit < 1 {
		limit = 1
	}
	w := int(clampPeerWnd(m.peerRecvWnd))
	if w < limit {
		limit = w
	}
	if limit > ARQWindow {
		limit = ARQWindow
	}
	return limit
}

func (m *Mux) sendWindowFullLocked() bool {
	inflight := int(m.sendNext - m.sendUnacked)
	return inflight >= m.sendLimitLocked() || len(m.sendBuf) >= ARQWindow
}

func (m *Mux) sendUnsequenced(ctx context.Context, f Frame) error {
	if m.initErr != nil {
		return m.initErr
	}
	if m.closed.Load() {
		return errMuxClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.Src = m.local
	f.Dest = m.remote
	f.Epoch = m.epoch
	f.Seq = 0
	wire, err := m.packFrame(f)
	if err != nil {
		return err
	}
	return m.carrier.Send(ctx, wire)
}

func (m *Mux) sendReliable(ctx context.Context, f Frame) error {
	for {
		if m.initErr != nil {
			return m.initErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.closed.Load() {
			return errMuxClosed
		}
		m.mu.Lock()
		if m.sendWindowFullLocked() {
			elapsed := time.Since(m.lastProgress)
			m.mu.Unlock()
			if elapsed >= arqStallTimeout {
				return errSendTimeout
			}
			timer := time.NewTimer(arqStallTimeout - elapsed)
			select {
			case <-ctx.Done():
				stopTimer(timer)
				return ctx.Err()
			case <-m.closeCh:
				stopTimer(timer)
				return errMuxClosed
			case <-m.sendWait:
				stopTimer(timer)
			case <-timer.C:
				return errSendTimeout
			}
			continue
		}
		seq := m.sendNext
		m.sendNext++
		src, dest, epoch := m.local, m.remote, m.epoch
		now := time.Now()
		if len(m.sendBuf) == 0 {
			m.lastProgress = now
		}
		slot := &sendSlot{seq: seq, frame: f, sentAt: now, firstAt: now}
		m.sendBuf[seq] = slot
		m.mu.Unlock()
		f.Src = src
		f.Dest = dest
		f.Epoch = epoch
		f.Seq = seq
		dataLen := 0
		if f.Type == TypeData {
			dataLen = len(f.Payload)
		}
		wire, err := m.packFrame(f)
		if err != nil {
			m.mu.Lock()
			if cur, ok := m.sendBuf[seq]; ok && cur == slot {
				delete(m.sendBuf, seq)
			}
			rewind := m.sendNext == seq+1
			if rewind {
				m.sendNext = seq
			}
			m.mu.Unlock()
			if !rewind {
				m.Close()
				return fmt.Errorf("wb1: pack failed, seq hole: %w", err)
			}
			m.wakeSend()
			return err
		}
		m.mu.Lock()
		if cur, ok := m.sendBuf[seq]; ok && cur == slot {
			slot.wire = wire
			slot.frame = f
		}
		m.mu.Unlock()
		if dataLen > 0 {
			m.addTraffic(int64(dataLen), 0)
		}
		_ = m.carrier.Send(ctx, wire)
		return nil
	}
}

func (m *Mux) wakeSend() {
	select {
	case m.sendWait <- struct{}{}:
	default:
	}
}

func (m *Mux) retransmitLoop(ctx context.Context) {
	tick := time.NewTicker(retransmitTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.closeCh:
			return
		case <-tick.C:
			if !m.retransmitDue(ctx) {
				m.Close()
				return
			}
		}
	}
}

func (m *Mux) retransmitDue(ctx context.Context) bool {
	now := time.Now()
	m.mu.Lock()
	rto := m.rto
	if rto < minRTO {
		rto = minRTO
	}
	if rto > maxRTO {
		rto = maxRTO
	}
	if len(m.sendBuf) > 0 && now.Sub(m.lastProgress) >= arqStallTimeout {
		staleFlight := false
		for _, slot := range m.sendBuf {
			if len(slot.wire) != 0 && now.Sub(slot.firstAt) >= arqStallTimeout {
				staleFlight = true
				break
			}
		}
		if staleFlight {
			m.mu.Unlock()
			return false
		}
	}
	type cand struct {
		seq  uint32
		slot *sendSlot
	}
	var due []cand
	for seq, slot := range m.sendBuf {
		if len(slot.wire) == 0 {
			continue
		}
		backoff := rto
		if slot.retries > 0 {
			shift := slot.retries
			if shift > 4 {
				shift = 4
			}
			backoff = rto << uint(shift)
			if backoff > maxRTO {
				backoff = maxRTO
			}
		}
		if now.Sub(slot.sentAt) >= backoff {
			due = append(due, cand{seq: seq, slot: slot})
		}
	}
	if len(due) == 0 {
		m.mu.Unlock()
		return true
	}
	sort.Slice(due, func(i, j int) bool { return due[i].seq < due[j].seq })
	if len(due) > maxRetransmitPerTick {
		due = due[:maxRetransmitPerTick]
	}
	if now.Sub(m.lastProgress) >= rto && !m.inRecovery {
		m.cwnd /= 2
		if m.cwnd < initialFlight {
			m.cwnd = initialFlight
		}
		m.inRecovery = true
	}
	wires := make([][]byte, 0, len(due))
	for _, d := range due {
		d.slot.retries++
		d.slot.sentAt = now
		d.slot.karn = true
		if len(d.slot.wire) > 0 {
			wires = append(wires, d.slot.wire)
		}
	}
	m.mu.Unlock()
	for _, wire := range wires {
		if m.closed.Load() || ctx.Err() != nil {
			return true
		}
		_ = m.carrier.Send(ctx, wire)
	}
	return true
}
