//go:build windows

package backend

import "syscall"

// watchNetworkChanges использует NotifyAddrChange (iphlpapi): вызов блокируется
// до изменения списка адресов (смена Wi-Fi/LTE, новый IP, DHCP). Фильтрацию по
// смене шлюза делает оркестратор.
func watchNetworkChanges(stop <-chan struct{}, onEvent func()) {
	iphlpapi := syscall.NewLazyDLL("iphlpapi.dll")
	notifyAddrChange := iphlpapi.NewProc("NotifyAddrChange")
	for {
		select {
		case <-stop:
			return
		default:
		}
		// Блокируется до следующего изменения адресов.
		notifyAddrChange.Call(0, 0)
		select {
		case <-stop:
			return
		default:
		}
		onEvent()
	}
}
