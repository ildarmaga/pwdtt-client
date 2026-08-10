package core

import (
	"errors"
	"io"
	"testing"
)

func TestIsRoutineRelayClose(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"EOF", io.EOF, true},
		{"eof substr", errors.New("read tcp: EOF"), true},
		{"use of closed", errors.New("use of closed network connection"), true},
		{"forcibly closed", errors.New("write tcp: An existing connection was forcibly closed by the remote host."), true},
		{"connection reset", errors.New("read: connection reset by peer"), true},
		{"wsasend", errors.New("wsasend: An existing connection was forcibly closed by the remote host."), true},
		{"WSASend case", errors.New("WSASend failed"), true},
		{"Connection Reset mixed", errors.New("Connection Reset"), true},
		{"unrelated", errors.New("dtls timeout"), false},
		{"quota", errors.New("TURN quota exceeded"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRoutineRelayClose(tc.err)
			if got != tc.want {
				t.Fatalf("isRoutineRelayClose(%v)=%v want %v", tc.err, got, tc.want)
			}
		})
	}
}
