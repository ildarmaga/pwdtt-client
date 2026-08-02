package wbtunnel

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
)

func TestCoalescedUDPStreamRequest(t *testing.T) {
	payload := []byte("dota-ping")
	reqJSON, err := json.Marshal(StreamRequest{Cmd: udpCommand, Addr: "103.10.124.1", Port: 27015})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.Write(reqJSON)
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload)))
	buf.Write(hdr[:])
	buf.Write(payload)

	dec := json.NewDecoder(&buf)
	var req StreamRequest
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Cmd != udpCommand || req.Port != 27015 {
		t.Fatalf("bad req: %+v", req)
	}
	got, err := readUDPPayload(io.MultiReader(dec.Buffered(), &buf))
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload=%q want %q", got, payload)
	}
}
