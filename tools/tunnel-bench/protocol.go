package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

const maxControlFrame = 64 << 10

const (
	opPing     = "ping"
	opDownload = "download"
	opUpload   = "upload"
)

type request struct {
	Token      string `json:"token"`
	Op         string `json:"op"`
	Bytes      int64  `json:"bytes,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
}

func writeJSON(w io.Writer, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > maxControlFrame {
		return fmt.Errorf("control frame too large: %d", len(b))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	if _, err = w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readJSON(r io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxControlFrame {
		return fmt.Errorf("invalid control frame size: %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	if err := json.Unmarshal(b, value); err != nil {
		return fmt.Errorf("decode control frame: %w", err)
	}
	return nil
}

func mbps(bytes int64, elapsed time.Duration) float64 {
	if bytes <= 0 || elapsed <= 0 {
		return 0
	}
	return float64(bytes*8) / elapsed.Seconds() / 1_000_000
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]time.Duration(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	if quantile <= 0 {
		return copyValues[0]
	}
	if quantile >= 1 {
		return copyValues[len(copyValues)-1]
	}
	index := int(float64(len(copyValues)-1)*quantile + 0.5)
	return copyValues[index]
}

func validateRequest(req request, token string, maxBytes int64) error {
	if token != "" && req.Token != token {
		return errors.New("unauthorized")
	}
	switch req.Op {
	case opPing:
		return nil
	case opDownload, opUpload:
		if req.Bytes <= 0 || req.Bytes > maxBytes {
			return fmt.Errorf("bytes must be in range 1..%d", maxBytes)
		}
		return nil
	default:
		return fmt.Errorf("unsupported operation %q", req.Op)
	}
}
