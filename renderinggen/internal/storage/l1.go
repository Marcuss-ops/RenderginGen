package storage

import "sync"

// memCache is the L1 VRAM cache: a bounded in-memory cache holding assets
// currently resident in GPU memory.
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.items[key]; exists {
		return
	}
	for m.max > 0 && m.size+int64(len(data)) > m.max && len(m.order) > 0 {
		oldest := m.order[0]
		m.order = m.order[1:]
		m.size -= int64(len(m.items[oldest]))
		delete(m.items, oldest)
	}
	m.items[key] = data
	m.order = append(m.order, key)
	m.size += int64(len(data))
}
