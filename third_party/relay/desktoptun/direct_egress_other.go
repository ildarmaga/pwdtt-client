//go:build !windows

package desktoptun

func DefaultLocalIPv4() string { return "" }

func DirectBypassHosts(...string) (func(), error) { return func() {}, nil }

func RememberPhysicalEgress(gateway, localIP string) {}
