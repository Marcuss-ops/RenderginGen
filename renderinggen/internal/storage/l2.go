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
