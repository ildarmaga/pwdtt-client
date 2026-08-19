package backend

import "net"

type trafficConn struct {
	net.Conn
	address string
}

func wrapTrafficConn(conn net.Conn, address string) net.Conn {
	return &trafficConn{Conn: conn, address: address}
}

func (c *trafficConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	observeNamedTraffic(c.address, int64(n), 0)
	return n, err
}

func (c *trafficConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	observeNamedTraffic(c.address, 0, int64(n))
	return n, err
}
