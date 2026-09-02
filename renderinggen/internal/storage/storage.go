// Package storage implements the shared artifact store with a three-level cache.
//
//	L1  small-object RAM cache (worker lifetime)
//	L2  local NVMe            (content-addressed on disk)
//	L3  object store           (central, shared, persistent)
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// Options configures the cache levels.
type Options struct {
	L1MaxBytes int64  // cap for the L1 in-memory small-object cache; 0 = unbounded
	L2Dir      string // on-disk (NVMe) cache directory
	L2MaxBytes int64  // cap for the L2 on-disk cache; 0 = unbounded
}

// Client resolves asset hashes against L3, caching them in L2 and L1.
type Client struct {
	backend Backend
	l1      *memCache
	l2      *diskCache

	l1Hits     atomic.Int64
	l2Hits     atomic.Int64
	l3Fetches  atomic.Int64
	inflightMu sync.Mutex
	inflight   map[string]*fetchCall
}

type fetchCall struct {
	done chan struct{}
	data []byte
	err  error
}

// CacheStats is a point-in-time snapshot of the resolution counters.
type CacheStats struct {
	L1Hits    int64
	L2Hits    int64
	L3Fetches int64
}

func (c *Client) Stats() CacheStats {
	return CacheStats{
		L1Hits:    c.l1Hits.Load(),
		L2Hits:    c.l2Hits.Load(),
		L3Fetches: c.l3Fetches.Load(),
	}
}

// New creates a cache client over the given L3 backend.
func New(backend Backend, opts Options) *Client {
	return &Client{
		backend:  backend,
		l1:       newMemCache(opts.L1MaxBytes),
		l2:       newDiskCache(opts.L2Dir, opts.L2MaxBytes),
		inflight: make(map[string]*fetchCall),
	}
}

// Open resolves a content hash to a streaming reader, preferring the local L2
// path so large media never needs to become a []byte in the worker.
func (c *Client) Open(ctx context.Context, hash string) (io.ReadCloser, int64, error) {
	if path, size, err := c.LocalPath(ctx, hash); err == nil {
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil, 0, openErr
		}
		return file, size, nil
	}
	if backend, ok := c.backend.(ReaderBackend); ok {
		return backend.FetchReader(ctx, hash)
	}
	data, err := c.Get(ctx, hash)
	if err != nil {
		return nil, 0, err
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

// Get is the compatibility byte API. New media paths should prefer LocalPath
// or Open so large files stay file-backed.
func (c *Client) Get(ctx context.Context, hash string) ([]byte, error) {
	if data, ok := c.l1.Get(hash); ok {
		c.l1Hits.Add(1)
		return data, nil
	}
	if data, ok := c.l2.Get(hash); ok {
		c.l2Hits.Add(1)
		c.l1.Put(hash, data)
		return data, nil
	}
	call, leader := c.startFetch(hash)
	if !leader {
		select {
		case <-call.done:
			if call.err != nil {
				return nil, call.err
			}
			return append([]byte(nil), call.data...), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	data, err := c.backend.Fetch(ctx, hash)
	if err != nil {
		c.finishFetch(hash, call, nil, err)
		return nil, err
	}
	c.l3Fetches.Add(1)
	c.l2.Put(hash, data)
	c.l1.Put(hash, data)
	c.finishFetch(hash, call, data, nil)
	return data, nil
}

func (c *Client) startFetch(hash string) (*fetchCall, bool) {
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	if call, ok := c.inflight[hash]; ok {
		return call, false
	}
	call := &fetchCall{done: make(chan struct{})}
	c.inflight[hash] = call
	return call, true
}

func (c *Client) finishFetch(hash string, call *fetchCall, data []byte, err error) {
	c.inflightMu.Lock()
	call.data = append([]byte(nil), data...)
	call.err = err
	delete(c.inflight, hash)
	close(call.done)
	c.inflightMu.Unlock()
}

// Put stores asset bytes into L3 and warms L2 and L1. It remains for small
// metadata and compatibility; large objects should use PutReader/PutFile.
func (c *Client) Put(ctx context.Context, hash string, data []byte) error {
	if err := c.backend.Store(ctx, hash, data); err != nil {
		return err
	}
	c.l2.Put(hash, data)
	c.l1.Put(hash, data)
	return nil
}

// PutReader stores a large object without buffering the complete payload in
// the Go heap. With an L2 cache it installs the stream once, then streams that
// file to L3; without L2 it streams directly to WriterBackend.
func (c *Client) PutReader(ctx context.Context, hash string, r io.Reader, size int64) error {
	if r == nil {
		return fmt.Errorf("storage: reader is required")
	}
	if backend, ok := c.backend.(WriterBackend); ok {
		if c.l2.dir == "" {
			return backend.StoreReader(ctx, hash, r, size)
		}
		path, actual, err := c.l2.InstallReader(hash, r, size)
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		storeErr := backend.StoreReader(ctx, hash, input, actual)
		closeErr := input.Close()
		if storeErr != nil {
			return storeErr
		}
		return closeErr
	}

	// Compatibility fallback for legacy byte-only backends.
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return c.Put(ctx, hash, data)
}

// LocalPath returns a verified local L2 path, fetching and atomically
// installing the object into L2 when necessary.
func (c *Client) LocalPath(ctx context.Context, hash string) (string, int64, error) {
	if c.l2.dir == "" {
		data, err := c.backend.Fetch(ctx, hash)
		if err == nil && len(hash) == 64 && Hash(data) != hash {
			return "", 0, fmt.Errorf("object hash mismatch: key %s", hash)
		}
		if err != nil {
			return "", 0, err
		}
		file, err := os.CreateTemp("", "renderinggen-local-path-*")
		if err != nil {
			return "", 0, err
		}
		path := file.Name()
		if _, err := file.Write(data); err != nil {
			file.Close()
			os.Remove(path)
			return "", 0, err
		}
		if err := file.Close(); err != nil {
			os.Remove(path)
			return "", 0, err
		}
		return path, int64(len(data)), nil
	}
	if path, size, err := c.l2.ContextPath(ctx, hash); err == nil {
		return path, size, nil
	}
	if backend, ok := c.backend.(ReaderBackend); ok {
		reader, size, err := backend.FetchReader(ctx, hash)
		if err != nil {
			return "", 0, err
		}
		defer reader.Close()
		path, actual, err := c.l2.InstallReader(hash, reader, size)
		if err != nil {
			return "", 0, err
		}
		c.l3Fetches.Add(1)
		return path, actual, nil
	}
	data, err := c.Get(ctx, hash)
	if err != nil {
		return "", 0, err
	}
	path, actual, err := c.l2.PutBytes(hash, data)
	if err != nil {
		return "", 0, err
	}
	return path, actual, nil
}

// PutFile publishes a local file through the streaming backend and then
// installs it into L2. The L2 install uses a hardlink when source and cache are
// on the same filesystem, falling back to a streaming copy otherwise.
func (c *Client) PutFile(ctx context.Context, hash, source string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("storage: source %s is not a regular file", source)
	}

	if backend, ok := c.backend.(WriterBackend); ok {
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		storeErr := backend.StoreReader(ctx, hash, input, info.Size())
		closeErr := input.Close()
		if storeErr != nil {
			return storeErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := c.backend.Store(ctx, hash, data); err != nil {
			return err
		}
	}

	if c.l2.dir != "" {
		_, _, err = c.l2.PutFile(hash, source)
		if err != nil {
			return err
		}
	}
	return nil
}

// Hash returns the content hash used as the cache key.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
