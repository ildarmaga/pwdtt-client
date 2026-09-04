package main

import (
	"bytes"
	"io"
	"net"
	"testing"
)

func TestServerPingDownloadUpload(t *testing.T) {
	tests := []struct {
		name string
		op   string
		size int64
	}{
		{name: "ping", op: opPing},
		{name: "download", op: opDownload, size: 256 << 10},
		{name: "upload", op: opUpload, size: 256 << 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			go handleConn(server, "token")
			defer client.Close()
			if err := writeJSON(client, request{Token: "token", Op: tc.op, Bytes: tc.size}); err != nil {
				t.Fatal(err)
			}
			var ready response
			if err := readJSON(client, &ready); err != nil || !ready.OK {
				t.Fatalf("ready=%+v err=%v", ready, err)
			}
			switch tc.op {
			case opDownload:
				n, err := io.CopyN(io.Discard, client, tc.size)
				if err != nil || n != tc.size {
					t.Fatalf("download n=%d err=%v", n, err)
				}
			case opUpload:
				n, err := io.CopyN(client, bytes.NewReader(make([]byte, tc.size)), tc.size)
				if err != nil || n != tc.size {
					t.Fatalf("upload n=%d err=%v", n, err)
				}
				var done response
				if err := readJSON(client, &done); err != nil || !done.OK || done.Bytes != tc.size {
					t.Fatalf("done=%+v err=%v", done, err)
				}
			}
		})
	}
}

func TestServerRejectsWrongToken(t *testing.T) {
	client, server := net.Pipe()
	go handleConn(server, "right")
	defer client.Close()
	if err := writeJSON(client, request{Token: "wrong", Op: opPing}); err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := readJSON(client, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Error != "unauthorized" {
		t.Fatalf("unexpected response %+v", resp)
	}
}
