package backend

import (
	"encoding/binary"
	"net"
	"sort"
	"strconv"
	"sync"
)

// TrafficConsumer is a destination seen inside the RAW tunnel. Only packet
// metadata is retained in memory; payload contents are never stored.
type TrafficConsumer struct {
	Address string `json:"address"`
	RxBytes int64  `json:"rxBytes"`
	TxBytes int64  `json:"txBytes"`
}

var tunnelConsumers = struct {
	sync.Mutex
	items map[string]*TrafficConsumer
}{items: make(map[string]*TrafficConsumer)}

const maxTunnelConsumerEntries = 4096

func resetTunnelConsumers() {
	tunnelConsumers.Lock()
	tunnelConsumers.items = make(map[string]*TrafficConsumer)
	tunnelConsumers.Unlock()
}

func observeTunnelPacket(pkt []byte, outbound bool) {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl {
		return
	}
	ipOff := 12
	if outbound {
		ipOff = 16
	}
	ip := net.IPv4(pkt[ipOff], pkt[ipOff+1], pkt[ipOff+2], pkt[ipOff+3]).String()
	address := ip
	if len(pkt) >= ihl+4 && (pkt[9] == 6 || pkt[9] == 17) {
		portOff := ihl
		if outbound {
			portOff += 2
		}
		port := binary.BigEndian.Uint16(pkt[portOff : portOff+2])
		if port != 0 {
			address += ":" + strconv.Itoa(int(port))
		}
	}

	tunnelConsumers.Lock()
	entry := tunnelConsumers.items[address]
	if entry == nil {
		if len(tunnelConsumers.items) >= maxTunnelConsumerEntries {
			address = "другие адреса"
			entry = tunnelConsumers.items[address]
		}
	}
	if entry == nil {
		entry = &TrafficConsumer{Address: address}
		tunnelConsumers.items[address] = entry
	}
	if outbound {
		entry.TxBytes += int64(len(pkt))
	} else {
		entry.RxBytes += int64(len(pkt))
	}
	tunnelConsumers.Unlock()
}

func snapshotTunnelConsumers(limit int) []TrafficConsumer {
	tunnelConsumers.Lock()
	out := make([]TrafficConsumer, 0, len(tunnelConsumers.items))
	for _, entry := range tunnelConsumers.items {
		out = append(out, *entry)
	}
	tunnelConsumers.Unlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].RxBytes+out[i].TxBytes > out[j].RxBytes+out[j].TxBytes
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (a *App) GetTrafficConsumers() []TrafficConsumer {
	return snapshotTunnelConsumers(20)
}
