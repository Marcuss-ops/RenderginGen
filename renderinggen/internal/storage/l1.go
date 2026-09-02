package storage

import "sync"

// DefaultL1MaxObjectBytes is the default per-object admission cap for the L1
// in-memory cache. Fonts, small images, LUTs and plan JSON belong in RAM;
// hundreds-of-MB video assets do not — for them the OS page cache over the L2
// NVMe file is the effective cache, and keeping their bytes on the Go heap
// only adds GC pressure. A true VRAM cache would hold decoded surfaces /
// texture handles, not []byte.
const DefaultL1MaxObjectBytes = 8 << 20 // 8 MiB

// memCache is the L1 cache: a bounded in-memory cache. Despite the
// historical "VRAM" name it lives on the Go heap; the admission policy below
// keeps it to small, high-reuse objects.
type memCache struct {
	mu    sync.Mutex
	items map[string][]byte
	order []string // FIFO order for eviction
	size  int64
	max   int64
	// maxObject is the per-object admission cap. Objects larger than this
	// are never stored in RAM (0 = no cap).
	maxObject int64
}

func newMemCache(max int64) *memCache {
	return newMemCacheWithObjectCap(max, DefaultL1MaxObjectBytes)
}

func newMemCacheWithObjectCap(max, maxObject int64) *memCache {
	return &memCache{items: make(map[string][]byte), max: max, maxObject: maxObject}
}

func (m *memCache) Get(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.items[key]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, true
}

func (m *memCache) Put(key string, data []byte) {
	dataLen := int64(len(data))
	if m.maxObject > 0 && int64(len(data)) > m.maxObject {
		// Large media stays on L2 NVMe (page cache); never on the Go heap.
		return
	}
	if m.max > 0 && dataLen > m.max {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.items[key]; exists {
		return
	}
	for m.max > 0 && m.size+dataLen > m.max && len(m.order) > 0 {
		oldest := m.order[0]
		m.order = m.order[1:]
		m.size -= int64(len(m.items[oldest]))
		delete(m.items, oldest)
	}
	// Own the cached bytes so callers cannot mutate an entry after Put.
	cached := append([]byte(nil), data...)
	m.items[key] = cached
	m.order = append(m.order, key)
	m.size += dataLen
}
