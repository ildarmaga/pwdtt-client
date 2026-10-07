package core

import (
	"net"
	"sync"
)

// Keep bootstrap addresses in memory while restarting workers with a retained
// TUN. Its private DNS cannot answer until those same workers have recovered.
type peerResolver struct {
	mu        sync.Mutex
	addresses map[string]net.UDPAddr
}

var sessionPeerResolver peerResolver

func (r *peerResolver) resolve(addr string, preserve bool, lookup func(string, string) (*net.UDPAddr, error)) (*net.UDPAddr, error) {
	if preserve {
		r.mu.Lock()
		cached, ok := r.addresses[addr]
		r.mu.Unlock()
		if ok {
			cached.IP = append(net.IP(nil), cached.IP...)
			return &cached, nil
		}
	}
	peer, err := lookup("udp", addr)
	if err != nil {
		return nil, err
	}
	copyPeer := *peer
	copyPeer.IP = append(net.IP(nil), peer.IP...)
	r.mu.Lock()
	if r.addresses == nil {
		r.addresses = make(map[string]net.UDPAddr)
	}
	r.addresses[addr] = copyPeer
	r.mu.Unlock()
	return peer, nil
}
