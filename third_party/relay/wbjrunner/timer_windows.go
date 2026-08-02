//go:build windows

package wbjrunner

import "golang.org/x/sys/windows"

// enableHighResTimer sets 1ms timer resolution so VP8 writerLoop pacing (~0.5ms
// tick) is not quantized to 15.6ms bursts (default on Windows).
func enableHighResTimer() func() {
	if err := windows.TimeBeginPeriod(1); err != nil {
		return func() {}
	}
	return func() { _ = windows.TimeEndPeriod(1) }
}
