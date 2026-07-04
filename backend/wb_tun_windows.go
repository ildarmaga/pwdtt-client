//go:build windows

package backend

func prepareWBTun() error {
	emergencyStopWBTun()
	return extractWintun()
}
