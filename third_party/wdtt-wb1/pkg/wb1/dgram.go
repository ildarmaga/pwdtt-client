package wb1

import (
	"encoding/binary"
	"io"
	"net"
)

const maxDatagram = 8192

func writeDgram(c net.Conn, p []byte) error {
	if len(p) > maxDatagram {
		p = p[:maxDatagram]
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(p)))
	if _, err := c.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.Write(p)
	return err
}

func readDgram(c net.Conn) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 {
		return []byte{}, nil
	}
	if n > maxDatagram {
		return nil, errPayloadTooBig
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// RelayDatagrams copies length-prefixed datagrams on a (mux stream) and
// one-datagram-per-Read/Write on a UDP conn.
func RelayDatagrams(stream, udp net.Conn) {
	defer stream.Close()
	defer udp.Close()
	done := make(chan struct{}, 2)
	go func() {
		for {
			d, err := readDgram(stream)
			if err != nil {
				break
			}
			if _, err := udp.Write(d); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, maxDatagram)
		for {
			n, err := udp.Read(buf)
			if err != nil {
				break
			}
			if n == 0 {
				continue
			}
			if err := writeDgram(stream, buf[:n]); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
}
