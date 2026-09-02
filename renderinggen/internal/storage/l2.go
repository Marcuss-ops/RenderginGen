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
)

// diskCache is the L2 NVMe cache: content-addressed files on disk.
// Usage is indexed in-memory after one lazy census. Normal writes therefore
// avoid a full filesystem walk; an ordered eviction scan only runs when the
// configured budget is actually exceeded.
type diskCache struct {
	dir string
	max int64
	mu  sync.Mutex

	usageReady bool
	current    int64
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
	if err := d.ensureUsageLocked(); err != nil {
		return "", 0, err
	}
	p := d.path(key)
	if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
		return p, info.Size(), nil
	}
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
	d.current += int64(len(data))
	if err := d.enforceBudgetLocked(); err != nil {
		return "", 0, err
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
	if err := d.ensureUsageLocked(); err != nil {
		return "", 0, err
	}
	p := d.path(key)
	if existing, err := os.Stat(p); err == nil && existing.Mode().IsRegular() {
		return p, existing.Size(), nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}

	// Same-filesystem CAS installs are metadata-only. This is the common
	// worker path when workspace and L2 live on the same NVMe volume.
	if err := os.Link(source, p); err == nil {
		d.current += info.Size()
		if err := d.enforceBudgetLocked(); err != nil {
			return "", 0, err
		}
		return p, info.Size(), nil
	}

	// Cross-device or unsupported hardlinks fall back to a single streaming
	// copy. No whole-file []byte allocation is used.
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
	d.current += info.Size()
	if err := d.enforceBudgetLocked(); err != nil {
		return "", 0, err
	}
	return p, info.Size(), nil
}

func (d *diskCache) InstallReader(key string, r io.Reader, size int64) (string, int64, error) {
	if d.dir == "" {
		return "", 0, fmt.Errorf("L2 cache directory is empty")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensureUsageLocked(); err != nil {
		return "", 0, err
	}
	p := d.path(key)
	if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
		return p, info.Size(), nil
	}
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
	d.current += written
	if err := d.enforceBudgetLocked(); err != nil {
		return "", 0, err
	}
	return p, written, nil
}

func (d *diskCache) Remove(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.path(key)
	if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
		if os.Remove(p) == nil && d.usageReady {
			d.current -= info.Size()
			if d.current < 0 {
				d.current = 0
			}
		}
	}
}

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
	_, _, _ = d.PutBytes(key, data)
}

func (d *diskCache) path(key string) string {
	prefix := key
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(d.dir, "assets", prefix, key)
}

// ensureUsageLocked performs the only unconditional filesystem census in the
// cache lifetime. Subsequent writes update d.current incrementally.
func (d *diskCache) ensureUsageLocked() error {
	if d.usageReady || d.dir == "" {
		return nil
	}
	var total int64
	err := filepath.Walk(d.dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	d.current = total
	d.usageReady = true
	return nil
}

// enforceBudgetLocked performs the expensive ordered scan only after the
// incremental counter says the cache is over budget. Callers must hold d.mu.
func (d *diskCache) enforceBudgetLocked() error {
	if d.max <= 0 || d.current <= d.max {
		return nil
	}
	type fi struct {
		path string
		size int64
		mod  int64
	}
	var files []fi
	if err := filepath.Walk(d.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		files = append(files, fi{path: path, size: info.Size(), mod: info.ModTime().UnixNano()})
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	for _, f := range files {
		if d.current <= d.max {
			break
		}
		if err := os.Remove(f.path); err == nil {
			d.current -= f.size
		}
	}
	if d.current < 0 {
		d.current = 0
	}
	return nil
}
