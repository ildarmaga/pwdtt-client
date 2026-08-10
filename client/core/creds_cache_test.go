package core

import "testing"

func TestGetCacheID_OneStreamPerCache(t *testing.T) {
	if streamsPerCache != 1 {
		t.Fatalf("streamsPerCache=%d, want 1 (each streamID own cache entry)", streamsPerCache)
	}
	ids := []int{0, 1, 9, 10, 99, 100, 101, 102, 103, 110}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if getCacheID(ids[i]) == getCacheID(ids[j]) {
				t.Fatalf("getCacheID(%d)==getCacheID(%d)==%d — adjacent pool slots must not share cache",
					ids[i], ids[j], getCacheID(ids[i]))
			}
		}
	}
	if getCacheID(100) == getCacheID(101) {
		t.Fatal("streamIDs 100 and 101 must not share cache")
	}
	if getCacheID(100) != 100 || getCacheID(101) != 101 {
		t.Fatalf("with streamsPerCache=1, getCacheID(n) must equal n; got 100→%d 101→%d",
			getCacheID(100), getCacheID(101))
	}
}

func TestGetStreamCache_DistinctEntries(t *testing.T) {
	c100 := getStreamCache(100)
	c101 := getStreamCache(101)
	c102 := getStreamCache(102)
	c103 := getStreamCache(103)
	if c100 == c101 || c100 == c102 || c101 == c103 {
		t.Fatal("streamIDs 100-103 must have distinct cache objects (not collapsed to cache=10)")
	}
	if getStreamCache(100) != c100 {
		t.Fatal("same streamID must return same cache pointer")
	}
}
