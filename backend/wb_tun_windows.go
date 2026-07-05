//go:build windows

package backend

func prepareWBTun() error {
	return extractWintun()
}
