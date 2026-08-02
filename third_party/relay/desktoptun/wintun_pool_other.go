//go:build !windows

package desktoptun

func QuickReleaseWintunPool(string) error { return nil }

func ForceReleaseWintunPool(string) error { return nil }
