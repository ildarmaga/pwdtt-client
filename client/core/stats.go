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
			totalMB := float64(up+down) / (1024.0 * 1024.0)
			logEmit("INFO", fmt.Sprintf("[СТАТИСТИКА] Активных: %d | Трафик: %.2f МБ", active, totalMB))
			turnMs := float64(atomic.LoadInt64(&s.TurnRTTNs)) / 1e6
			dtlsMs := float64(atomic.LoadInt64(&s.DTLSHSNs)) / 1e6
			if statsEmit != nil {
				statsEmit(down, up, active, turnMs, dtlsMs)
			}
		}
	}
}
