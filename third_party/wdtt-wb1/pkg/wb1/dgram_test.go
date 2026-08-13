package wb1

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestWriteReadDgram(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() {
		if err := writeDgram(a, []byte("hello")); err != nil {
			t.Errorf("write: %v", err)
		}
	}()
	got, err := readDgram(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestRelayDatagramsUDP(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := echo.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echo.WriteTo(append([]byte("R"), buf[:n]...), addr)
		}
	}()

	left, right := net.Pipe()
	defer left.Close()
	udp, err := net.Dial("udp", echo.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	go RelayDatagrams(right, udp)

	if err := writeDgram(left, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = left.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := readDgram(left)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Rping" {
		t.Fatalf("got %q", got)
	}
	_ = left.Close()
	_, _ = io.Copy(io.Discard, left)
}
