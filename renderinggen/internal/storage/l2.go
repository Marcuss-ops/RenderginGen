package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
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
	digest := sha256.New()
	return d.install(key, func(tmp *os.File) (int64, error) {
		written, err := io.Copy(io.MultiWriter(tmp, digest), r)
		if err != nil {
			return 0, err
		}
		if size >= 0 && written != size {
			return 0, fmt.Errorf("L3 size %d does not match expected %d", written, size)
		}
		if got := hex.EncodeToString(digest.Sum(nil)); len(key) == 64 && got != key {
			return 0, fmt.Errorf("L3 hash %s does not match key %s", got, key)
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

// enforceBudget evicts the oldest indexed entries. It intentionally does not
// census the filesystem on every write; startup files are lazily indexed when
// accessed and stale entries are harmlessly skipped. The caller holds d.mu;
// only metadata operations (unlink) happen here.
func (d *diskCache) enforceBudget() {
	type candidate struct {
		key   string
		entry cacheEntry
	}
	candidates := make([]candidate, 0, len(d.entries))
	for key, entry := range d.entries {
		candidates = append(candidates, candidate{key, entry})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].entry.accessed.Before(candidates[j].entry.accessed) })
	for _, c := range candidates {
		if d.total <= d.max {
			break
		}
		// Only account a successful removal: a failed delete (foreign holder,
		// read-only directory) must not desynchronize the byte budget.
		if err := os.Remove(d.path(c.key)); err == nil {
			d.total -= c.entry.size
			delete(d.entries, c.key)
		} else if !os.IsNotExist(err) {
			// Unremovable file: forget the index entry so the next eviction
			// pass can make progress on other candidates instead of stalling.
			delete(d.entries, c.key)
			d.total -= c.entry.size
		}
	}
}
