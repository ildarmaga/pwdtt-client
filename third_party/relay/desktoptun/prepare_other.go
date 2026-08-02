//go:build !windows

package desktoptun

func PrepareBeforeStart(string) {}

func CleanupAllWDTTAdapters() {}

func TeardownTunAdapter(string) {}

func ReleaseWintunPool(string) error { return nil }

func EnsureAdapterAbsent(string) error { return nil }

func AdapterPresent(string) bool { return false }

func DeepPrepareBeforeStart(string) {}
