package wbtunnel

import (
	"net"

	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
)

// statsConn wraps a tunneled smux stream and feeds sessionstats (rx/tx bytes).
type statsConn struct {
	net.Conn
}

func (c *statsConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		sessionstats.AddRx(uint64(n))
	}
	return n, err
}

func (c *statsConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		sessionstats.AddTx(uint64(n))
	}
	return n, err
}

// streamConn is a statsConn that releases a concurrency slot on Close.
type streamConn struct {
	statsConn
	release func()
}

func (c *streamConn) Close() error {
	err := c.statsConn.Close()
	if c.release != nil {
		c.release()
		c.release = nil
	}
	return err
}
