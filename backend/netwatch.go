package backend

import (
	"fmt"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// startNetworkWatch запускает слежение за сменой сети ОС (Wi-Fi↔LTE, новый Wi-Fi,
// смена шлюза). При реальной смене апстрим-шлюза немедленно инициирует полный
// reconnect — это в разы быстрее реактивного детекта (залипание 8 c / probe).
// Идея взята из VK (Cronet NetworkChangeNotifier + connection migration).
func (o *Orchestrator) startNetworkWatch() {
	o.stopNetworkWatch()
	stop := make(chan struct{})
	o.netWatchStop = stop

	events := make(chan struct{}, 16)
	go watchNetworkChanges(stop, func() {
		select {
		case events <- struct{}{}:
		default:
		}
	})

	go func() {
		var timer *time.Timer
		var timerC <-chan time.Time
		for {
			select {
			case <-stop:
				if timer != nil {
					timer.Stop()
				}
				return
			case <-events:
				// Дебаунс: коалесцируем всплеск событий (DHCP, поднятие интерфейсов).
				if timer == nil {
					timer = time.NewTimer(networkChangeDebounce)
					timerC = timer.C
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(networkChangeDebounce)
				}
			case <-timerC:
				timer = nil
				timerC = nil
				o.onNetworkSettled()
			}
		}
	}()
}

func (o *Orchestrator) stopNetworkWatch() {
	if o.netWatchStop != nil {
		close(o.netWatchStop)
		o.netWatchStop = nil
	}
}

// onNetworkSettled вызывается после затихания всплеска сетевых событий.
// Реагируем только на реальную смену шлюза по умолчанию — события от нашего же
// wg-turn шлюз не меняют и игнорируются.
func (o *Orchestrator) onNetworkSettled() {
	o.mu.Lock()
	up := o.tunnelUp
	o.mu.Unlock()
	if !up {
		return
	}
	newGW := defaultGateway()
	if newGW == "" {
		return // апстрим пропал — этим займутся probe/stall-детекторы
	}
	vkRouteMu.Lock()
	oldGW := wgGateway
	vkRouteMu.Unlock()
	if oldGW == "" || newGW == oldGW {
		return // шлюз не менялся — ложное срабатывание
	}
	runtime.EventsEmit(o.appCtx, "log", "INFO",
		fmt.Sprintf("[NET] Смена сети: шлюз %s → %s", oldGW, newGW))
	o.triggerReconnect("Смена сети — переподключение", true)
}
