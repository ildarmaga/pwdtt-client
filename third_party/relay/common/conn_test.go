package common

import (
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
)

func TestIsBenignConnError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"EOF", io.EOF, true},
		{"ErrClosed", net.ErrClosed, true},
		{"use of closed", errors.New("read tcp 127.0.0.1:1->127.0.0.1:2: use of closed network connection"), true},
		{"reset by peer", errors.New("read: connection reset by peer"), true},
		{"windows forcibly closed", errors.New("read tcp 127.0.0.1:10809->127.0.0.1:57745: wsarecv: An existing connection was forcibly closed by the remote host."), true},
		{"wsarecv alone", errors.New("wsarecv: something"), true},
		{"real failure", fmt.Errorf("dial tcp: no route to host"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsBenignConnError(tc.err); got != tc.want {
				t.Fatalf("IsBenignConnError(%v)=%v want %v", tc.err, got, tc.want)
			}
		})
	}
}
