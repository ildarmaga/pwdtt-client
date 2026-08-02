package wbtunnel

import (
	"encoding/binary"
	"testing"
)

func TestScaleKCPWnd(t *testing.T) {
	if got := scaleKCPWnd(2048, 300, 8192); got <= 2048 || got > 8192 {
		t.Fatalf("scaleKCPWnd(2048,300)= %d want (2048,8192]", got)
	}
	if got := scaleKCPWnd(2048, 50, 8192); got != 2048 {
		t.Fatalf("low RTT should stay base: got %d", got)
	}
}

func TestAppendWireBatching(t *testing.T) {
	var batch []byte
	appendWire := func(pkt []byte) {
		wireLen := 2 + len(pkt)
		off := len(batch)
		need := off + wireLen
		if cap(batch) < need {
			nb := make([]byte, off, need)
			copy(nb, batch)
			batch = nb
		}
		batch = batch[:need]
		binary.BigEndian.PutUint16(batch[off:off+2], uint16(len(pkt)))
		copy(batch[off+2:], pkt)
	}
	appendWire([]byte("aa"))
	appendWire([]byte("bbb"))
	if len(batch) != 2+2+2+3 {
		t.Fatalf("batch len=%d", len(batch))
	}
	n1 := int(binary.BigEndian.Uint16(batch[0:2]))
	if n1 != 2 || string(batch[2:4]) != "aa" {
		t.Fatalf("first frame bad: n=%d body=%q", n1, batch[2:4])
	}
	n2 := int(binary.BigEndian.Uint16(batch[4:6]))
	if n2 != 3 || string(batch[6:9]) != "bbb" {
		t.Fatalf("second frame bad: n=%d body=%q", n2, batch[6:9])
	}
}
