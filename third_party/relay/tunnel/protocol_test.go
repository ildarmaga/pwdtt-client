package tunnel

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeFrameRoundTrip(t *testing.T) {
	payload := []byte("hello-tunnel")
	frame := EncodeFrame(42, MsgData, payload)

	var gotConnID uint32
	var gotType byte
	var gotPayload []byte
	DecodeFrames(frame, func(connID uint32, msgType byte, p []byte) {
		gotConnID = connID
		gotType = msgType
		gotPayload = append([]byte(nil), p...)
	})
	if gotConnID != 42 || gotType != MsgData || !bytes.Equal(gotPayload, payload) {
		t.Fatalf("decode mismatch conn=%d type=%d payload=%q", gotConnID, gotType, gotPayload)
	}
}

func TestDecodeFramesMultipleInOneBlob(t *testing.T) {
	f1 := EncodeFrame(1, MsgConnect, []byte("a"))
	f2 := EncodeFrame(2, MsgData, []byte("bb"))
	f3 := EncodeFrame(3, MsgClose, nil)
	blob := append(append(f1, f2...), f3...)

	var n int
	DecodeFrames(blob, func(connID uint32, msgType byte, payload []byte) {
		n++
		switch n {
		case 1:
			if connID != 1 || msgType != MsgConnect || string(payload) != "a" {
				t.Fatalf("frame1: conn=%d type=%d payload=%q", connID, msgType, payload)
			}
		case 2:
			if connID != 2 || msgType != MsgData || string(payload) != "bb" {
				t.Fatalf("frame2: conn=%d type=%d payload=%q", connID, msgType, payload)
			}
		case 3:
			if connID != 3 || msgType != MsgClose || len(payload) != 0 {
				t.Fatalf("frame3: conn=%d type=%d payload=%q", connID, msgType, payload)
			}
		default:
			t.Fatalf("unexpected extra frame #%d", n)
		}
	})
	if n != 3 {
		t.Fatalf("decoded %d frames, want 3", n)
	}
}

func TestEncodeDecodeVP8Config(t *testing.T) {
	frame := EncodeVP8Config(30, 64, 2)
	var payload []byte
	DecodeFrames(frame, func(_ uint32, msgType byte, p []byte) {
		if msgType != MsgConfig {
			t.Fatalf("msgType=%d want MsgConfig", msgType)
		}
		payload = p
	})
	fps, batch, tracks, ok := DecodeVP8Config(payload)
	if !ok || fps != 30 || batch != 64 || tracks != 2 {
		t.Fatalf("config fps=%d batch=%d tracks=%d ok=%v", fps, batch, tracks, ok)
	}
}
