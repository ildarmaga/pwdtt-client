package core

import "testing"

func TestActiveTURNWorkersLifecycle(t *testing.T) {
	const url = "192.0.2.240:19302"
	noteRelayReady(url, 1)
	noteRelayReady(url, 1)
	defer noteRelayReady(url, -2)
	snapshot := ActiveTURNWorkers()
	if snapshot[url] != 2 {
		t.Fatalf("workers=%d", snapshot[url])
	}
	snapshot[url] = 99
	if ActiveTURNWorkers()[url] != 2 {
		t.Fatal("snapshot changed live counters")
	}
	noteRelayReady(url, -1)
	if ActiveTURNWorkers()[url] != 1 {
		t.Fatal("disconnect not counted")
	}
	noteRelayReady(url, -1)
	if _, exists := ActiveTURNWorkers()[url]; exists {
		t.Fatal("ended relay still shown as connected")
	}
}
