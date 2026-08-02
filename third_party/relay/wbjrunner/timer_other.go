//go:build !windows

package wbjrunner

func enableHighResTimer() func() { return func() {} }
