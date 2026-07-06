//go:build !windows

package backend

func prepareWBXray() error { return nil }

func xrayBinaryPath() (string, error) {
	return "", nil
}
