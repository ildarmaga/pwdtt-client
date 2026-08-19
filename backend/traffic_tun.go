package backend

import "golang.zx2c4.com/wireguard/tun"

type trafficTun struct{ tun.Device }

func wrapTrafficTun(device tun.Device) tun.Device { return &trafficTun{Device: device} }

func (t *trafficTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	n, err := t.Device.Read(bufs, sizes, offset)
	for i := 0; i < n && i < len(sizes) && i < len(bufs); i++ {
		if sizes[i] > 0 && offset+sizes[i] <= len(bufs[i]) {
			observeTunnelPacket(bufs[i][offset:offset+sizes[i]], true)
		}
	}
	return n, err
}

func (t *trafficTun) Write(bufs [][]byte, offset int) (int, error) {
	n, err := t.Device.Write(bufs, offset)
	if err == nil {
		for i := 0; i < n && i < len(bufs); i++ {
			if offset <= len(bufs[i]) {
				observeTunnelPacket(bufs[i][offset:], false)
			}
		}
	}
	return n, err
}
