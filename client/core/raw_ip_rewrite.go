package core

import (
	"encoding/binary"
	"net"
)

// rewriteIPv4SrcInPlace меняет source IP (для uplink: primary TUN → IP воркера).
func rewriteIPv4SrcInPlace(pkt []byte, newSrc net.IP) bool {
	src4 := newSrc.To4()
	if src4 == nil || len(pkt) < 20 || pkt[0]>>4 != 4 {
		return false
	}
	copy(pkt[12:16], src4)
	fixIPv4Checksums(pkt)
	return true
}

// rewriteIPv4DstInPlace меняет destination IP (для downlink: IP воркера → primary TUN).
func rewriteIPv4DstInPlace(pkt []byte, newDst net.IP) bool {
	dst4 := newDst.To4()
	if dst4 == nil || len(pkt) < 20 || pkt[0]>>4 != 4 {
		return false
	}
	copy(pkt[16:20], dst4)
	fixIPv4Checksums(pkt)
	return true
}

func fixIPv4Checksums(pkt []byte) {
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl {
		return
	}
	binary.BigEndian.PutUint16(pkt[10:12], 0)
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:ihl]))

	proto := pkt[9]
	payload := pkt[ihl:]
	src := pkt[12:16]
	dst := pkt[16:20]
	switch proto {
	case 6: // TCP
		if len(payload) < 18 {
			return
		}
		binary.BigEndian.PutUint16(payload[16:18], 0)
		sum := transportChecksum(src, dst, 6, payload)
		binary.BigEndian.PutUint16(payload[16:18], sum)
	case 17: // UDP
		if len(payload) < 8 {
			return
		}
		binary.BigEndian.PutUint16(payload[6:8], 0)
		sum := transportChecksum(src, dst, 17, payload)
		if sum == 0 {
			sum = 0xffff // UDP: 0 means no checksum; use ffff
		}
		binary.BigEndian.PutUint16(payload[6:8], sum)
	}
}

func ipChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func transportChecksum(src, dst []byte, proto byte, payload []byte) uint16 {
	var sum uint32
	sum += uint32(binary.BigEndian.Uint16(src[0:2]))
	sum += uint32(binary.BigEndian.Uint16(src[2:4]))
	sum += uint32(binary.BigEndian.Uint16(dst[0:2]))
	sum += uint32(binary.BigEndian.Uint16(dst[2:4]))
	sum += uint32(proto)
	sum += uint32(len(payload))
	for i := 0; i+1 < len(payload); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(payload[i:]))
	}
	if len(payload)%2 == 1 {
		sum += uint32(payload[len(payload)-1]) << 8
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// parseRawConfIP extracts IPv4 from RAWCONF response body.
func parseRawConfIP(conf string) net.IP {
	for _, line := range splitLines(conf) {
		line = trimSpaceASCII(line)
		if line == "" || line[0] == '#' {
			continue
		}
		eq := -1
		for i := 0; i < len(line); i++ {
			if line[i] == '=' {
				eq = i
				break
			}
		}
		if eq < 0 {
			continue
		}
		key := trimSpaceASCII(line[:eq])
		val := trimSpaceASCII(line[eq+1:])
		if !eqFoldASCII(key, "ip") && !eqFoldASCII(key, "address") {
			continue
		}
		if i := indexByte(val, '/'); i >= 0 {
			val = val[:i]
		}
		ip := net.ParseIP(val)
		if ip4 := ip.To4(); ip4 != nil {
			return ip4
		}
	}
	return nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpaceASCII(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

func eqFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
