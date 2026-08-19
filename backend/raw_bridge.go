package backend

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/tun"
)

// Headroom for Linux virtioNetHdr (≥10). Harmless on Windows wintun.
const rawTunWriteOffset = 16

var (
	rawBridgeMu   sync.Mutex
	rawBridgeStop chan struct{}
	rawBridgeWG   sync.WaitGroup
	rawBridgeUDP  *net.UDPConn
)

func stopRawBridge() {
	rawBridgeMu.Lock()
	stopCh := rawBridgeStop
	uc := rawBridgeUDP
	rawBridgeStop = nil
	rawBridgeUDP = nil
	rawBridgeMu.Unlock()
	if stopCh != nil {
		close(stopCh)
	}
	if uc != nil {
		_ = uc.Close()
	}
	rawBridgeWG.Wait()
}

// rebindRawBridgeSoft — после SoftReconnect core слушает новый UDP :9000,
// а старый Dial в bridge мёртв. Переподключаем TUN↔dispatcher без сноса интерфейса.
func rebindRawBridgeSoft(listenPort string) error {
	tunDev := rawSoftTun()
	if tunDev == nil {
		return fmt.Errorf("нет активного RAW TUN")
	}
	if listenPort == "" {
		listenPort = "9000"
	}
	if err := startRawBridge(tunDev, listenPort); err != nil {
		return err
	}
	log.Printf("[RAW] Soft-reconnect: bridge переподключён к 127.0.0.1:%s", listenPort)
	return nil
}

// startRawBridge pipes IPv4 packets between TUN and local UDP dispatcher (127.0.0.1:listenPort).
func startRawBridge(tunDev tun.Device, listenPort string) error {
	stopRawBridge()
	if listenPort == "" {
		listenPort = "9000"
	}
	dst, err := net.ResolveUDPAddr("udp", "127.0.0.1:"+listenPort)
	if err != nil {
		return err
	}
	uc, err := net.DialUDP("udp", nil, dst)
	if err != nil {
		return err
	}
	stopCh := make(chan struct{})
	rawBridgeMu.Lock()
	rawBridgeStop = stopCh
	rawBridgeUDP = uc
	rawBridgeMu.Unlock()

	batch := tunDev.BatchSize()
	if batch < 1 {
		batch = 1
	}

	rawBridgeWG.Add(2)
	go func() {
		defer rawBridgeWG.Done()
		bufs := make([][]byte, batch)
		sizes := make([]int, batch)
		for i := range bufs {
			bufs[i] = make([]byte, 2048)
		}
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			n, err := tunDev.Read(bufs, sizes, 0)
			if err != nil {
				return
			}
			for i := 0; i < n; i++ {
				pkt := bufs[i][:sizes[i]]
				if len(pkt) < 20 || pkt[0]>>4 != 4 {
					continue
				}
				observeTunnelPacket(pkt, true)
				if _, err := uc.Write(pkt); err != nil {
					return
				}
			}
		}
	}()

	go func() {
		defer rawBridgeWG.Done()
		buf := make([]byte, 2048)
		for {
			_ = uc.SetReadDeadline(time.Now().Add(2 * time.Second))
			nr, err := uc.Read(buf)
			if err != nil {
				select {
				case <-stopCh:
					return
				default:
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						continue
					}
					return
				}
			}
			if nr < 20 || buf[0]>>4 != 4 {
				continue
			}
			observeTunnelPacket(buf[:nr], false)
			pkt := make([]byte, rawTunWriteOffset+nr)
			copy(pkt[rawTunWriteOffset:], buf[:nr])
			if _, err := tunDev.Write([][]byte{pkt}, rawTunWriteOffset); err != nil {
				return
			}
		}
	}()

	log.Printf("[RAW] Bridge TUN ↔ 127.0.0.1:%s", listenPort)
	return nil
}
