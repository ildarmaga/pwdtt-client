package wbtunnel

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/ildarmaga/whitelist-bypass/relay/common"
	"github.com/xtaci/smux"
)

const udpCommand = "udp"

// StreamRequest is the JSON header on each new smux stream (TCP connect or UDP datagram).
type StreamRequest struct {
	Cmd  string `json:"cmd"`
	Addr string `json:"addr"`
	Port int    `json:"port"`
}

func parseStreamRequest(buf []byte) (StreamRequest, bool) {
	var req StreamRequest
	if err := json.Unmarshal(buf, &req); err != nil {
		return req, false
	}
	switch req.Cmd {
	case connectCommand, udpCommand:
		return req, true
	default:
		return req, false
	}
}

func writeUDPRequest(stream *smux.Stream, addr string, port int, payload []byte) error {
	req, err := json.Marshal(StreamRequest{Cmd: udpCommand, Addr: addr, Port: port})
	if err != nil {
		return err
	}
	_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := stream.Write(req); err != nil {
		return err
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload)))
	if _, err := stream.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := stream.Write(payload); err != nil {
			return err
		}
	}
	_ = stream.SetWriteDeadline(time.Time{})
	return nil
}

func readUDPResponse(stream *smux.Stream, timeout time.Duration) ([]byte, error) {
	_ = stream.SetReadDeadline(time.Now().Add(timeout))
	defer stream.SetReadDeadline(time.Time{})
	var hdr [2]byte
	if _, err := io.ReadFull(stream, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 {
		return nil, fmt.Errorf("empty udp response")
	}
	if n > common.UDPBufSize {
		return nil, fmt.Errorf("udp response too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(stream, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func readUDPPayload(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 {
		return nil, nil
	}
	if n > common.UDPBufSize {
		return nil, fmt.Errorf("udp payload too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeUDPResponse(stream *smux.Stream, payload []byte) error {
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload)))
	if _, err := stream.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := stream.Write(payload); err != nil {
			return err
		}
	}
	return nil
}
