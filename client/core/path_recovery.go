package core

import (
	"context"
	"time"
)

func runPathRepairs(ctx context.Context, requests <-chan struct{}, repair func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-requests:
			if ctx.Err() != nil {
				return
			}
			repair()
		}
	}
}

const pathRepairAfter = 15 * time.Second

type pathRecoveryAction int

const (
	pathHealthy pathRecoveryAction = iota
	pathWaiting
	pathRepair
	pathRestart
)

// Outgoing UDP writes are deliberately excluded: they cannot prove delivery.
type pathRecovery struct {
	recovering bool
	lastRepair time.Time
}

func (p *pathRecovery) check(now, lastInbound time.Time) pathRecoveryAction {
	idle := now.Sub(lastInbound)
	if idle < pathRepairAfter {
		p.recovering = false
		p.lastRepair = time.Time{}
		return pathHealthy
	}
	p.recovering = true
	if idle >= consentTimeout {
		return pathRestart
	}
	if p.lastRepair.IsZero() || now.Sub(p.lastRepair) >= pathRepairAfter {
		p.lastRepair = now
		return pathRepair
	}
	return pathWaiting
}
