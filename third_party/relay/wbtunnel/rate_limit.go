package wbtunnel

import (
	"context"
	"io"

	"golang.org/x/time/rate"
)

// mbpsToLimiter builds a token bucket for MB/s (megabytes/sec). nil = unlimited.
func mbpsToLimiter(mbPerSec float64) *rate.Limiter {
	if mbPerSec <= 0 {
		return nil
	}
	bytesPerSec := mbPerSec * 1024 * 1024
	// ~250ms of data as burst so small limits still throttle promptly.
	burst := int(bytesPerSec * 0.25)
	if burst < 4*1024 {
		burst = 4 * 1024
	}
	if burst > 256*1024 {
		burst = 256 * 1024
	}
	return rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

// limitedWriter throttles Write via WaitN (chunks to limiter burst).
type limitedWriter struct {
	ctx    context.Context
	w      io.Writer
	getLim func() *rate.Limiter
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	total := 0
	for len(p) > 0 {
		n := len(p)
		if lim := lw.getLim(); lim != nil {
			burst := lim.Burst()
			if burst > 0 && n > burst {
				n = burst
			}
			if err := lim.WaitN(lw.ctx, n); err != nil {
				return total, err
			}
		}
		nw, err := lw.w.Write(p[:n])
		total += nw
		if err != nil {
			return total, err
		}
		if nw == 0 {
			return total, io.ErrShortWrite
		}
		p = p[nw:]
	}
	return total, nil
}
