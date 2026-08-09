package core

import (
	"fmt"
	"sync/atomic"
	"time"
)

type Stats struct {
	ActiveConnections int32
	TotalBytesUp      int64
	TotalBytesDown    int64
	DroppedUp         int64 // RAW sticky: uplink drops under SendCh backpressure
	TurnRTTNs         int64
	DTLSHSNs          int64
}

func NewStats() *Stats {
	return &Stats{}
}

func (s *Stats) RunLoop(shutdown <-chan struct{}, logEmit func(level, msg string), statsEmit func(rx, tx int64, workers int32, turnRttMs, dtlsHsMs float64)) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-shutdown:
			return
		case <-ticker.C:
			active := atomic.LoadInt32(&s.ActiveConnections)
			up := atomic.LoadInt64(&s.TotalBytesUp)
			down := atomic.LoadInt64(&s.TotalBytesDown)
			dropUp := atomic.SwapInt64(&s.DroppedUp, 0)
			totalMB := float64(up+down) / (1024.0 * 1024.0)
			if dropUp > 0 {
				logEmit("INFO", fmt.Sprintf("[СТАТ] Активных: %d | Трафик: %.2f МБ | drop_up=%d", active, totalMB, dropUp))
			} else {
				logEmit("INFO", fmt.Sprintf("[СТАТ] Активных: %d | Трафик: %.2f МБ", active, totalMB))
			}
			turnMs := float64(atomic.LoadInt64(&s.TurnRTTNs)) / 1e6
			dtlsMs := float64(atomic.LoadInt64(&s.DTLSHSNs)) / 1e6
			if statsEmit != nil {
				statsEmit(down, up, active, turnMs, dtlsMs)
			}
		}
	}
}
