package storage

import "sync"

const defaultL1MaxObjectBytes int64 = 4 << 20 // 4 MiB: metadata, fonts and small images only

// memCache is a bounded small-object RAM cache. Large media stays file-backed
// in L2/NVMe and relies on the OS page cache; []byte here is not GPU/VRAM
// residency and must not be used as a video cache.
type memCache struct {
	mu    sync.Mutex
	items map[string][]byte
	order []string // FIFO order for eviction
	size  int64
	max   int64
}

func newMemCache(max int64) *memCache {
	return &memCache{items: make(map[string][]byte), max: max}
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
	if dataLen > defaultL1MaxObjectBytes || (m.max > 0 && dataLen > m.max) {
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
