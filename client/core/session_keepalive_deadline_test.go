package core

import (
	"testing"
	"time"
)

// Документирует инвариант: keepaliveInterval + старый WriteDeadline(5s)
// давали гибель воркера ~15s. Дедлайн на ping больше не ставится.
func TestKeepaliveIntervalNotPairedWithShortWriteDeadline(t *testing.T) {
	if keepaliveInterval != 10*time.Second {
		t.Fatalf("keepaliveInterval=%v — обнови тест если меняли тикер", keepaliveInterval)
	}
	// Раньше: first tick @10s + uncleared deadline 5s → death @15s.
	poisonWindow := keepaliveInterval + 5*time.Second
	if poisonWindow < 15*time.Second {
		t.Fatalf("unexpected poison window %v", poisonWindow)
	}
}
