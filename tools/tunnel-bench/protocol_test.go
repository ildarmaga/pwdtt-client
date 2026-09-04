package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestRequestRoundTrip(t *testing.T) {
	want := request{Token: "secret", Op: opDownload, Bytes: 8 << 20, DurationMS: 2500}
	var wire bytes.Buffer
	if err := writeJSON(&wire, want); err != nil {
		t.Fatal(err)
	}
	var got request
	if err := readJSON(&wire, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestReadJSONRejectsOversizedFrame(t *testing.T) {
	var wire bytes.Buffer
	wire.Write([]byte{0, 16, 0, 1})
	wire.Write(make([]byte, 16))
	var got request
	if err := readJSON(&wire, &got); err == nil {
		t.Fatal("expected oversized frame error")
	}
}

func TestMbps(t *testing.T) {
	if got := mbps(10_000_000, 2*time.Second); got != 40 {
		t.Fatalf("got %.2f want 40", got)
	}
}

func TestPercentile(t *testing.T) {
	values := []time.Duration{5 * time.Millisecond, time.Millisecond, 3 * time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}
	if got := percentile(values, 0.95); got != 5*time.Millisecond {
		t.Fatalf("got %s want 5ms", got)
	}
}

func TestReportJSONDoesNotExposeTokenOrProxyPassword(t *testing.T) {
	_, safeProxy, err := makeDialer("socks5://user:password@127.0.0.1:10809")
	if err != nil {
		t.Fatal(err)
	}
	r := report{Protocol: "raw", Proxy: safeProxy, DownloadMbps: 12.5}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("password")) {
		t.Fatalf("report leaked proxy password: %s", b)
	}
}
