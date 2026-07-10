package backend

import "testing"

func TestClassifyWBLog_benignSOCKS(t *testing.T) {
	cases := []struct {
		raw  string
		emit bool
		lvl  string
	}{
		{
			raw:  `relay: SOCKS 4 read error: read tcp 127.0.x.x:10809->127.0.x.x:57745: use of closed network connection (sent 2 times, 1824B)`,
			emit: false,
		},
		{
			raw:  `relay: SOCKS 19 read error: read tcp 127.0.x.x:10809->127.0.x.x:53627: wsarecv: An existing connection was forcibly closed by the remote host. (sent 8 times, 3970B)`,
			emit: false,
		},
		{
			raw:  `relay: SOCKS CONNECTED 1 -> w***:443 rdy_wait=51ms`,
			emit: true,
			lvl:  "INFO",
		},
		{
			raw:  `relay: SOCKS 9 read error: dial tcp: no route to host (sent 1 times, 0B)`,
			emit: true,
			lvl:  "ERROR",
		},
	}
	for _, tc := range cases {
		lvl, _, ok := classifyWBLog(tc.raw)
		if ok != tc.emit {
			t.Fatalf("%q emit=%v want %v", tc.raw, ok, tc.emit)
		}
		if tc.emit && lvl != tc.lvl {
			t.Fatalf("%q level=%s want %s", tc.raw, lvl, tc.lvl)
		}
	}
}
