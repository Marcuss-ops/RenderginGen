package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/hashio"
)

// diskCache is the L2 NVMe cache: content-addressed files on disk.
//
// The mutex guards only the in-memory index (entries/total) and the short
// commit phase (existence check + rename + eviction bookkeeping). All large
// I/O — streaming a fetched object to disk, hashing it, copying a file — runs
// WITHOUT the lock, so concurrent installs of different assets proceed in
// parallel instead of serializing behind whichever goroutine is copying the
// biggest file.
type diskCache struct {
	dir     string
	max     int64
	mu      sync.Mutex
	entries map[string]cacheEntry
	total   int64
}

type cacheEntry struct {
	size     int64
	accessed time.Time
}

func newDiskCache(dir string, max int64) *diskCache {
	return &diskCache{dir: dir, max: max, entries: make(map[string]cacheEntry)}
}

func (d *diskCache) Get(key string) ([]byte, bool) {
	data, err := os.ReadFile(d.path(key))
	if err != nil {
		return nil, false
	}
	d.mu.Lock()
	d.entries[key] = cacheEntry{size: int64(len(data)), accessed: time.Now()}
	d.mu.Unlock()
	return data, true
}

func (d *diskCache) Path(key string) (string, bool) {
	p := d.path(key)
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return p, true
}

// install writes an object into the cache through a temp file and commits it
// atomically. write receives the open temp file and must return the number of
// bytes written; it runs WITHOUT the lock (it may stream arbitrarily large
// objects). The commit phase (rename + index + eviction) is short and runs
// under the lock. A concurrent install of the same content address simply
// bumps the access time of the existing entry and discards the redundant temp
// file.
func (d *diskCache) install(key string, write func(*os.File) (int64, error)) (string, int64, error) {
	if d.dir == "" {
		return "", 0, fmt.Errorf("L2 cache directory is empty")
	}
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".install-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()
	size, err := write(tmp)
	if err != nil {
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.entries[key]; ok {
		// A concurrent install won the race for the same content address; the
		// bytes are identical by construction (the key is the content hash).
		d.entries[key] = cacheEntry{size: prev.size, accessed: time.Now()}
		return p, prev.size, nil
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return "", 0, err
	}
	committed = true
	d.entries[key] = cacheEntry{size: size, accessed: time.Now()}
	d.total += size
	if d.max > 0 {
		d.enforceBudget()
	}
	return p, size, nil
}

func (d *diskCache) PutBytes(key string, data []byte) (string, int64, error) {
	return d.install(key, func(tmp *os.File) (int64, error) {
		n, err := tmp.Write(data)
		return int64(n), err
	})
}

func (d *diskCache) PutFile(key, source string) (string, int64, error) {
	if d.dir == "" {
		return "", 0, fmt.Errorf("L2 cache directory is empty")
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("source %s is not a regular file", source)
	}
	return d.install(key, func(tmp *os.File) (int64, error) {
		input, err := os.Open(source)
		if err != nil {
			return 0, err
		}
		n, copyErr := io.Copy(tmp, input)
		closeErr := input.Close()
		if copyErr != nil {
			return 0, copyErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
		return n, nil
	})
}

func (d *diskCache) InstallReader(key string, r io.Reader, size int64) (string, int64, error) {
	return d.install(key, func(tmp *os.File) (int64, error) {
		digest, written, err := hashio.Copy(r, tmp)
		if err != nil {
			return 0, err
		}
		if size >= 0 && written != size {
			return 0, fmt.Errorf("L3 size %d does not match expected %d", written, size)
		}
		if len(key) == 64 && digest != key {
			return 0, fmt.Errorf("L3 hash %s does not match key %s", digest, key)
		}
		return written, nil
	})
}

func (d *diskCache) Remove(key string) { _ = os.Remove(d.path(key)) }

func (d *diskCache) ContextPath(ctx context.Context, key string) (string, int64, error) {
	select {
	case <-ctx.Done():
		return "", 0, ctx.Err()
	default:
	}
	p, ok := d.Path(key)
	if !ok {
		return "", 0, ErrNotFound
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", 0, err
	}
	return p, info.Size(), nil
}

// Put stores object bytes in the cache (byte-slice API). An empty cache
// directory means the cache level is disabled, so the call is a no-op — the
// caller's L3 write already succeeded. Any real write error is returned so
// the cache cannot degrade silently.
func (d *diskCache) Put(key string, data []byte) error {
	if d.dir == "" {
		return nil
	}
	_, _, err := d.PutBytes(key, data)
	return err
}

func (d *diskCache) path(key string) string {
	prefix := key
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(d.dir, "assets", prefix, key)
}

// evictionBatch bounds the work of a single budget-enforcement pass. The
// historical implementation snapshotted the whole index and sorted it O(n log n)
// under d.mu on every over-budget install — with ~100k small entries one pass
// became a multi-millisecond stall of every concurrent cache user plus a full
// candidate-slice allocation in the path that is supposed to be cheap. Each
// pass now removes at most evictionBatch of the oldest entries via an
// allocation-free scan, so worst-case per-install stall is bounded. When one
// install overflows the budget by more than a batch can reclaim, subsequent
// installs finish the eviction (amortized); accounting stays exact.
const evictionBatch = 64

// enforceBudget evicts the oldest indexed entries. It intentionally does not
// census the filesystem on every write; startup files are lazily indexed when
// accessed and stale entries are harmlessly skipped. The caller holds d.mu;
// only metadata operations (unlink) happen here.
func (d *diskCache) enforceBudget() {
	for evicted := 0; d.total > d.max && evicted < evictionBatch; evicted++ {
		// Allocation-free single-oldest scan: a full map snapshot + global sort
		// per pass would defeat the bounded-batch purpose.
		var oldestKey string
		var oldestAccessed time.Time
		found := false
		for key, entry := range d.entries {
			if !found || entry.accessed.Before(oldestAccessed) {
				oldestKey, oldestAccessed, found = key, entry.accessed, true
			}
		}
		if !found {
			return
		}
		entry := d.entries[oldestKey]
		err := os.Remove(d.path(oldestKey))
		switch {
		case err == nil || os.IsNotExist(err):
			// The bytes are gone from the cache either way: account them. A
			// vanished file (external cleanup) must not leave a stale index
			// entry that every future pass re-scans forever.
			delete(d.entries, oldestKey)
			d.total -= entry.size
		default:
			// Unremovable file (held open by a renderer, read-only directory):
			// forget the index entry so the pass can make progress on other
			// candidates instead of stalling. The bytes stay on disk but are no
			// longer accounted — say so instead of silently growing the gap
			// between d.total and the real disk usage.
			delete(d.entries, oldestKey)
			d.total -= entry.size
			log.Printf("storage: L2 eviction could not remove %s: %v (index entry forgotten; cache budget may under-report disk usage)", d.path(oldestKey), err)
		}
	}
}
