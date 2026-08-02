package sessionstats

import (
	"io"
	"sync/atomic"
)

var (
	rxBytes atomic.Uint64
	txBytes atomic.Uint64
	rttMs   atomic.Uint32
)

func Reset() {
	rxBytes.Store(0)
	txBytes.Store(0)
	rttMs.Store(0)
}

func AddRx(n uint64) {
	if n > 0 {
		rxBytes.Add(n)
	}
}

func AddTx(n uint64) {
	if n > 0 {
		txBytes.Add(n)
	}
}

func SetRTT(ms int) {
	if ms < 0 {
		ms = 0
	}
	rttMs.Store(uint32(ms))
}

func Snapshot() (rx, tx uint64, rtt int) {
	return rxBytes.Load(), txBytes.Load(), int(rttMs.Load())
}

func Copy(dst io.Writer, src io.Reader, countRx bool) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				total += int64(nw)
				if countRx {
					AddRx(uint64(nw))
				} else {
					AddTx(uint64(nw))
				}
			}
			if ew != nil {
				return total, ew
			}
			if nr != nw {
				return total, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return total, nil
			}
			return total, er
		}
	}
}
