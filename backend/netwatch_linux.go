//go:build linux

package backend

import (
	"sync"
	"syscall"
)

// Группы мультикаста rtnetlink (не экспортируются пакетом syscall).
const (
	rtmgrpLink       = 0x1
	rtmgrpIPv4IfAddr = 0x10
	rtmgrpIPv4Route  = 0x40
)

// watchNetworkChanges слушает rtnetlink и вызывает onEvent на любое изменение
// интерфейсов/адресов/маршрутов. Фильтрацию (смена шлюза) делает оркестратор.
func watchNetworkChanges(stop <-chan struct{}, onEvent func()) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW, syscall.NETLINK_ROUTE)
	if err != nil {
		return
	}
	var once sync.Once
	closeFd := func() { once.Do(func() { syscall.Close(fd) }) }
	defer closeFd()

	addr := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: rtmgrpLink | rtmgrpIPv4IfAddr | rtmgrpIPv4Route,
	}
	if err := syscall.Bind(fd, addr); err != nil {
		return
	}

	// Закрытие сокета по stop разблокирует Recvfrom.
	go func() {
		<-stop
		closeFd()
	}()

	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			return // сокет закрыт (stop) или ошибка
		}
		if n > 0 {
			onEvent()
		}
	}
}
