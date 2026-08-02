package common

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"time"
)

// EnableTCPNoDelay disables Nagle on TCP connections when supported.
func EnableTCPNoDelay(conn net.Conn) {
	type noDelay interface {
		SetNoDelay(bool) error
	}
	var visit func(net.Conn) bool
	visit = func(c net.Conn) bool {
		if c == nil {
			return false
		}
		if nd, ok := c.(noDelay); ok {
			_ = nd.SetNoDelay(true)
			return true
		}
		if u, ok := c.(interface{ Underlying() net.Conn }); ok {
			return visit(u.Underlying())
		}
		return false
	}
	_ = visit(conn)
}

// EnableTCPKeepAlive turns on kernel TCP keepalive when conn is (or wraps) *net.TCPConn.
func EnableTCPKeepAlive(conn net.Conn, period time.Duration) {
	if period <= 0 {
		period = 30 * time.Second
	}
	type keepAlive interface {
		SetKeepAlive(bool) error
		SetKeepAlivePeriod(time.Duration) error
	}
	var set func(net.Conn) bool
	var visit func(net.Conn) bool
	visit = func(c net.Conn) bool {
		if c == nil {
			return false
		}
		if ka, ok := c.(keepAlive); ok {
			_ = ka.SetKeepAlive(true)
			_ = ka.SetKeepAlivePeriod(period)
			return true
		}
		if u, ok := c.(interface{ Underlying() net.Conn }); ok {
			return visit(u.Underlying())
		}
		return false
	}
	set = visit
	_ = set(conn)
}

// IsBenignConnError reports expected connection teardown errors (not worth alarming).
func IsBenignConnError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "i/o timeout") ||
		// Windows (wsarecv / WSAECONNRESET) — app/CDN closed the socket.
		strings.Contains(msg, "forcibly closed by the remote host") ||
		strings.Contains(msg, "wsarecv")
}
