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

func (d *diskCache) PutBytes(key string, data []byte) (string, int64, error) {
	if d.dir == "" {
		return "", 0, fmt.Errorf("L2 cache directory is empty")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".install-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return "", 0, err
	}
	if previous, ok := d.entries[key]; ok {
		d.total -= previous.size
	}
	d.entries[key] = cacheEntry{size: int64(len(data)), accessed: time.Now()}
	d.total += int64(len(data))
	if d.max > 0 {
		d.enforceBudget()
	}
	return p, int64(len(data)), nil
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
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".install-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	input, err := os.Open(source)
	if err != nil {
		tmp.Close()
		return "", 0, err
	}
	_, copyErr := io.Copy(tmp, input)
	closeInputErr := input.Close()
	if copyErr == nil {
		copyErr = closeInputErr
	}
	if copyErr == nil {
		copyErr = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if copyErr != nil {
		return "", 0, copyErr
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return "", 0, err
	}
	if previous, ok := d.entries[key]; ok {
		d.total -= previous.size
	}
	d.entries[key] = cacheEntry{size: info.Size(), accessed: time.Now()}
	d.total += info.Size()
	if d.max > 0 {
		d.enforceBudget()
	}
	return p, info.Size(), nil
}

func (d *diskCache) InstallReader(key string, r io.Reader, size int64) (string, int64, error) {
	if d.dir == "" {
		return "", 0, fmt.Errorf("L2 cache directory is empty")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".fetch-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, digest), r)
	if err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err != nil {
		return "", 0, err
	}
	if size >= 0 && written != size {
		return "", 0, fmt.Errorf("L3 size %d does not match expected %d", written, size)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); len(key) == 64 && got != key {
		return "", 0, fmt.Errorf("L3 hash %s does not match key %s", got, key)
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return "", 0, err
	}
	if previous, ok := d.entries[key]; ok {
		d.total -= previous.size
	}
	d.entries[key] = cacheEntry{size: written, accessed: time.Now()}
	d.total += written
	if d.max > 0 {
		d.enforceBudget()
	}
	return p, written, nil
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
	if previous, ok := d.entries[key]; ok {
		d.total -= previous.size
	}
	d.entries[key] = cacheEntry{size: int64(len(data)), accessed: time.Now()}
	d.total += int64(len(data))
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

// enforceBudget evicts the oldest indexed entries. It intentionally does not
// census the filesystem on every write; startup files are lazily indexed when
// accessed and stale entries are harmlessly skipped.
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
