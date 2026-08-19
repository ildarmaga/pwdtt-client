package backend

import (
	"encoding/binary"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// TrafficConsumer is a remote endpoint seen in the active tunnel. Only
// counters and endpoint metadata are retained in memory; payloads are not.
type TrafficConsumer struct {
	Address string `json:"address"`
	Domain  string `json:"domain"`
	RxBytes int64  `json:"rxBytes"`
	TxBytes int64  `json:"txBytes"`
}

var tunnelConsumers = struct {
	sync.Mutex
	items map[string]*TrafficConsumer
	names map[string]string
}{items: make(map[string]*TrafficConsumer), names: make(map[string]string)}

const maxTunnelConsumerEntries = 4096

func resetTunnelConsumers() {
	tunnelConsumers.Lock()
	tunnelConsumers.items = make(map[string]*TrafficConsumer)
	tunnelConsumers.names = make(map[string]string)
	tunnelConsumers.Unlock()
}

func observeNamedTraffic(address string, rxBytes, txBytes int64) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	domain := ""
	if net.ParseIP(strings.Trim(host, "[]")) == nil {
		domain = host
	}
	addTraffic(address, domain, rxBytes, txBytes)
}

func addTraffic(address, domain string, rxBytes, txBytes int64) {
	if address == "" || (rxBytes <= 0 && txBytes <= 0) {
		return
	}
	tunnelConsumers.Lock()
	entry := tunnelConsumers.items[address]
	if entry == nil && len(tunnelConsumers.items) >= maxTunnelConsumerEntries {
		address, domain = "другие адреса", ""
		entry = tunnelConsumers.items[address]
	}
	if entry == nil {
		entry = &TrafficConsumer{Address: address, Domain: domain}
		tunnelConsumers.items[address] = entry
	} else if entry.Domain == "" && domain != "" {
		entry.Domain = domain
	}
	entry.RxBytes += rxBytes
	entry.TxBytes += txBytes
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
	protocol := pkt[9]
	if protocol == 17 && len(pkt) >= ihl+8 {
		srcPort := binary.BigEndian.Uint16(pkt[ihl : ihl+2])
		dstPort := binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4])
		if srcPort == 53 || dstPort == 53 {
			observeDNSMessage(pkt[ihl+8:])
		}
	}
	ipOff, portOff := 12, ihl
	if outbound {
		ipOff, portOff = 16, ihl+2
	}
	ip := net.IPv4(pkt[ipOff], pkt[ipOff+1], pkt[ipOff+2], pkt[ipOff+3]).String()
	address := ip
	if len(pkt) >= ihl+4 && (protocol == 6 || protocol == 17) {
		port := binary.BigEndian.Uint16(pkt[portOff : portOff+2])
		if port != 0 {
			address += ":" + strconv.Itoa(int(port))
		}
	}
	tunnelConsumers.Lock()
	domain := tunnelConsumers.names[ip]
	tunnelConsumers.Unlock()
	if outbound {
		addTraffic(address, domain, 0, int64(len(pkt)))
	} else {
		addTraffic(address, domain, int64(len(pkt)), 0)
	}
}

// observeDNSMessage learns A-records from DNS responses already passing through
// the tunnel. It never performs reverse DNS or sends data anywhere.
func observeDNSMessage(msg []byte) {
	if len(msg) < 12 || msg[2]&0x80 == 0 {
		return
	}
	questions := int(binary.BigEndian.Uint16(msg[4:6]))
	answers := int(binary.BigEndian.Uint16(msg[6:8]))
	off, name := 12, ""
	for i := 0; i < questions; i++ {
		var next int
		var ok bool
		name, next, ok = dnsName(msg, off, 0)
		if !ok || next+4 > len(msg) {
			return
		}
		off = next + 4
	}
	for i := 0; i < answers; i++ {
		_, next, ok := dnsName(msg, off, 0)
		if !ok || next+10 > len(msg) {
			return
		}
		typ := binary.BigEndian.Uint16(msg[next : next+2])
		rdlen := int(binary.BigEndian.Uint16(msg[next+8 : next+10]))
		rdata := next + 10
		if rdata+rdlen > len(msg) {
			return
		}
		if typ == 1 && rdlen == 4 && name != "" {
			ip := net.IPv4(msg[rdata], msg[rdata+1], msg[rdata+2], msg[rdata+3]).String()
			tunnelConsumers.Lock()
			tunnelConsumers.names[ip] = strings.TrimSuffix(name, ".")
			tunnelConsumers.Unlock()
		}
		off = rdata + rdlen
	}
}

func dnsName(msg []byte, off, depth int) (string, int, bool) {
	if depth > 8 || off >= len(msg) {
		return "", off, false
	}
	labels := make([]string, 0, 4)
	for {
		if off >= len(msg) {
			return "", off, false
		}
		n := int(msg[off])
		if n&0xc0 == 0xc0 {
			if off+1 >= len(msg) {
				return "", off, false
			}
			ptr := ((n & 0x3f) << 8) | int(msg[off+1])
			tail, _, ok := dnsName(msg, ptr, depth+1)
			if !ok {
				return "", off, false
			}
			if tail != "" {
				labels = append(labels, tail)
			}
			return strings.Join(labels, "."), off + 2, true
		}
		off++
		if n == 0 {
			return strings.Join(labels, "."), off, true
		}
		if n > 63 || off+n > len(msg) {
			return "", off, false
		}
		labels = append(labels, string(msg[off:off+n]))
		off += n
	}
}

func snapshotTunnelConsumers(limit int) []TrafficConsumer {
	tunnelConsumers.Lock()
	out := make([]TrafficConsumer, 0, len(tunnelConsumers.items))
	for _, entry := range tunnelConsumers.items {
		copy := *entry
		if copy.Domain == "" {
			host, _, err := net.SplitHostPort(copy.Address)
			if err == nil {
				copy.Domain = tunnelConsumers.names[host]
			}
		}
		out = append(out, copy)
	}
	tunnelConsumers.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].RxBytes+out[i].TxBytes > out[j].RxBytes+out[j].TxBytes })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (a *App) GetTrafficConsumers() []TrafficConsumer { return snapshotTunnelConsumers(20) }
