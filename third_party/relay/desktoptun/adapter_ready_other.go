//go:build !windows

package desktoptun

import "time"

func WintunAdapterSnapshot() string { return "" }

func EnsureTunAdapterReady(_ string) bool { return true }

func WaitAdapterPresent(_ string, _ time.Duration) error { return nil }

// WaitAdapterUp is a no-op on non-Windows hosts.
func WaitAdapterUp(_ string, _ time.Duration) error { return nil }

// WaitTunRoutingReady is a no-op on non-Windows hosts.
func WaitTunRoutingReady(_ string, _ time.Duration) error { return nil }
