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
	if outbound && protocol == 6 && len(pkt) >= ihl+20 {
		tcpLen := int(pkt[ihl+12]>>4) * 4
		if tcpLen >= 20 && len(pkt) > ihl+tcpLen {
			if domain := packetDomain(pkt[ihl+tcpLen:]); domain != "" {
				ip := net.IPv4(pkt[16], pkt[17], pkt[18], pkt[19]).String()
				rememberTrafficName(ip, domain)
			}
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

func rememberTrafficName(ip, domain string) {
	domain = strings.TrimSpace(strings.TrimSuffix(domain, "."))
	if ip == "" || domain == "" || net.ParseIP(domain) != nil {
		return
	}
	tunnelConsumers.Lock()
	tunnelConsumers.names[ip] = domain
	for _, entry := range tunnelConsumers.items {
		host, _, err := net.SplitHostPort(entry.Address)
		if err == nil && host == ip {
			entry.Domain = domain
		}
	}
	tunnelConsumers.Unlock()
}

func packetDomain(payload []byte) string {
	if domain := tlsServerName(payload); domain != "" {
		return domain
	}
	// Plain HTTP and proxy-style requests. Headers are ASCII and end at CRLFCRLF.
	if len(payload) > 8192 {
		payload = payload[:8192]
	}
	text := string(payload)
	for _, line := range strings.Split(text, "\r\n") {
		if len(line) >= 5 && strings.EqualFold(line[:5], "host:") {
			host := strings.TrimSpace(line[5:])
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			return host
		}
	}
	return ""
}

// tlsServerName extracts SNI from a complete TLS ClientHello carried by the
// packet. It reads only protocol metadata and never retains payload contents.
func tlsServerName(p []byte) string {
	if len(p) < 5 || p[0] != 22 {
		return ""
	}
	recordLen := int(binary.BigEndian.Uint16(p[3:5]))
	if recordLen < 4 {
		return ""
	}
	recordEnd := 5 + recordLen
	if recordEnd > len(p) {
		recordEnd = len(p)
	}
	h := p[5:recordEnd]
	if len(h) < 4 || h[0] != 1 {
		return ""
	}
	handshakeLen := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
	if handshakeLen < 38 {
		return ""
	}
	handshakeEnd := 4 + handshakeLen
	if handshakeEnd > len(h) {
		handshakeEnd = len(h)
	}
	b := h[4:handshakeEnd]
	off := 2 + 32
	if off >= len(b) {
		return ""
	}
	sessionLen := int(b[off])
	off += 1 + sessionLen
	if off+2 > len(b) {
		return ""
	}
	cipherLen := int(binary.BigEndian.Uint16(b[off : off+2]))
	off += 2 + cipherLen
	if off >= len(b) {
		return ""
	}
	compressionLen := int(b[off])
	off += 1 + compressionLen
	if off+2 > len(b) {
		return ""
	}
	extensionsLen := int(binary.BigEndian.Uint16(b[off : off+2]))
	off += 2
	end := off + extensionsLen
	if end > len(b) {
		end = len(b)
	}
	for off+4 <= end {
		typ := binary.BigEndian.Uint16(b[off : off+2])
		length := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
		off += 4
		if off+length > end {
			return ""
		}
		if typ == 0 && length >= 5 {
			ext := b[off : off+length]
			listLen := int(binary.BigEndian.Uint16(ext[0:2]))
			if listLen+2 <= len(ext) && ext[2] == 0 {
				nameLen := int(binary.BigEndian.Uint16(ext[3:5]))
				if 5+nameLen <= len(ext) {
					return string(ext[5 : 5+nameLen])
				}
			}
		}
		off += length
	}
	return ""
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
			rememberTrafficName(ip, name)
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
