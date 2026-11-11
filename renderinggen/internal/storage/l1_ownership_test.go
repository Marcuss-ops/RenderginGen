package storage

import "testing"

// TestL1GetReturnsDefensiveCopy pins the ownership contract of the in-memory
// cache: a caller may mutate the slice it received without changing what any
// later caller reads. Returning the cache's own slice made the documented
// "treat as read-only" rule unenforceable and turned a caller write into
// shared-state corruption (and a data race against concurrent readers).
func TestL1GetReturnsDefensiveCopy(t *testing.T) {
	m := newMemCache(1 << 20)
	original := []byte("original cached bytes")
	m.Put("key", original)

	first, ok := m.Get("key")
	if !ok {
		t.Fatal("Get missed a just-inserted entry")
	}
	first[0] = 'X'
	first[len(first)-1] = 'Y'

	second, ok := m.Get("key")
	if !ok {
		t.Fatal("Get missed after mutating a returned slice")
	}
	if string(second) != string(original) {
		t.Fatalf("cache entry changed through a returned slice: got %q, want %q", second, original)
	}

	// Put owns its input too: mutating the caller's slice after Put must not
	// change the cached entry.
	m.Put("second", []byte("put-owned"))
	stored, _ := m.Get("second")
	stored[0] = 'Z'
	again, _ := m.Get("second")
	if string(again) != "put-owned" {
		t.Fatalf("Put retained the caller's buffer: got %q", again)
	}
}

// TestL1PerObjectCapRejectsOversizeObjects pins the admission half of the L1
// design: objects above the per-object cap are never stored, even when the
// total budget has room for them. Without this, one media asset could occupy
// the Go heap the cache exists to protect.
func TestL1PerObjectCapRejectsOversizeObjects(t *testing.T) {
	m := newMemCacheWithObjectCap(1<<20, 4)
	m.Put("too-big", []byte("12345"))
	if _, ok := m.Get("too-big"); ok {
		t.Fatal("L1 admitted an object above its per-object cap")
	}

	m.Put("exact", []byte("1234"))
	if data, ok := m.Get("exact"); !ok || string(data) != "1234" {
		t.Fatalf("L1 refused an object exactly at the cap: ok=%v data=%q", ok, data)
	}
	if m.size != 4 {
		t.Fatalf("L1 accounting = %d after admitting 4 bytes, want 4", m.size)
	}
}
