//go:build windows

package backend

import (
	"github.com/ildarmaga/whitelist-bypass/relay/desktoptun"
)

func emergencyStopWBTun() {
	desktoptun.CleanupAllWDTTAdapters()
}
