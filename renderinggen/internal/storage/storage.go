// Package storage implements the shared artifact store with a three-level cache.
//
//	L1  VRAM cache   (in-memory, worker lifetime)
//	L2  local NVMe   (content-addressed on disk)
//	L3  object store (central, shared, persistent)
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Options configures the cache levels.
type Options struct {
	L1MaxBytes int64  // cap for the L1 in-memory (VRAM) cache; 0 = unbounded
	L2Dir      string // on-disk (NVMe) cache directory
	L2MaxBytes int64  // cap for the L2 on-disk cache; 0 = unbounded
}

// Client resolves asset hashes against L3, caching them in L2 and L1.
type Client struct {
	backend Backend
	l1      *memCache
	l2      *diskCache
}

// New creates a cache client over the given L3 backend.
func New(backend Backend, opts Options) *Client {
	return &Client{
		backend: backend,
		l1:      newMemCache(opts.L1MaxBytes),
		l2:      newDiskCache(opts.L2Dir, opts.L2MaxBytes),
	}
}

// Get returns asset bytes for hash, resolving L1 -> L2 -> L3 and promoting
// on each miss so the next lookup is faster.
func (c *Client) Get(ctx context.Context, hash string) ([]byte, error) {
	if data, ok := c.l1.Get(hash); ok {
		return data, nil
	}
	if data, ok := c.l2.Get(hash); ok {
		c.l1.Put(hash, data)
		return data, nil
	}
	data, err := c.backend.Fetch(ctx, hash)
	if err != nil {
		return nil, err
	}
	c.l2.Put(hash, data)
	c.l1.Put(hash, data)
	return data, nil
}

// Put stores asset bytes into L3 (source of truth) and warms L2 and L1.
func (c *Client) Put(ctx context.Context, hash string, data []byte) error {
	if err := c.backend.Store(ctx, hash, data); err != nil {
		return err
	}
	c.l2.Put(hash, data)
	c.l1.Put(hash, data)
	return nil
}

// Hash returns the content hash used as the cache key.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
