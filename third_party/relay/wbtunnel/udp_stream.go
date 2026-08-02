package wbtunnel

import (
	"sync"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/ildarmaga/whitelist-bypass/relay/common/sessionstats"
	"github.com/xtaci/smux"
)

const (
	udpInboundQueue = 64
	udpRelayIdle    = 120 * time.Second
)

// streamUDPInbound reads length-prefixed datagrams from smux and delivers them
// to ch until the stream ends or stop is closed.
func streamUDPInbound(stream *smux.Stream, ch chan []byte, stop <-chan struct{}) {
	defer func() {
		select {
		case ch <- nil:
		default:
		}
	}()
	for {
		select {
		case <-stop:
			return
		default:
		}
		payload, err := readUDPPayload(stream)
		if err != nil {
			return
		}
		if len(payload) == 0 {
			continue
		}
		sessionstats.AddRx(uint64(len(payload)))
		msg := append([]byte(nil), payload...)
		select {
		case ch <- msg:
		case <-stop:
			return
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- msg:
			case <-stop:
				return
			}
		}
	}
}

// relayUDPUpstream pumps datagrams from a UDP socket (or SOCKS UDP) onto smux.
func relayUDPUpstream(stream *smux.Stream, stop <-chan struct{}, readFn func([]byte) (int, error)) {
	buf := make([]byte, common.UDPBufSize)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := readFn(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := writeUDPResponse(stream, buf[:n]); err != nil {
			return
		}
		_ = stream.SetWriteDeadline(time.Time{})
	}
}

type udpStream struct {
	stream   *smux.Stream
	inbound  chan []byte
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

func newUDPStream(stream *smux.Stream) *udpStream {
	s := &udpStream{
		stream:  stream,
		inbound: make(chan []byte, udpInboundQueue),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go func() {
		streamUDPInbound(stream, s.inbound, s.stop)
		close(s.done)
	}()
	return s
}

func (s *udpStream) Close() {
	s.stopOnce.Do(func() {
		close(s.stop)
		_ = s.stream.Close()
		<-s.done
	})
}
