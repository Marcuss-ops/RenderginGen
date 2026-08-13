package storage

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// diskCache is the L2 NVMe cache: content-addressed files on disk.
type diskCache struct {
	dir string
	max int64
	mu  sync.Mutex
}

func newDiskCache(dir string, max int64) *diskCache {
	return &diskCache{dir: dir, max: max}
}

func (d *diskCache) Get(key string) ([]byte, bool) {
	data, err := os.ReadFile(d.path(key))
	if err != nil {
		return nil, false
	}
	return data, true
}

func (d *diskCache) Put(key string, data []byte) {
	if d.dir == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return
	}
	if d.max > 0 {
		d.enforceBudget()
	}
}

func (d *diskCache) path(key string) string {
	prefix := key
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(d.dir, "assets", prefix, key)
}

// enforceBudget removes oldest files until total size is within d.max.
// Callers must hold d.mu.
func (d *diskCache) enforceBudget() {
	type fi struct {
		path string
		size int64
		mod  int64
	}
	var files []fi
	var total int64
	_ = filepath.Walk(d.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		files = append(files, fi{path: path, size: info.Size(), mod: info.ModTime().UnixNano()})
		total += info.Size()
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	for _, f := range files {
		if total <= d.max {
			break
		}
		_ = os.Remove(f.path)
		total -= f.size
	}
}
