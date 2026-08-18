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
	"sync"
	"sync/atomic"
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

	// Observability counters, incremented by Get/Put so benchmarks and the
	// daemon comparison can report L3->L2->L1 promotion and cache hit rates.
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
	L1Hits    int64 // resolved from L1 (in-memory)
	L2Hits    int64 // resolved from L2 (on-disk)
	L3Fetches int64 // resolved from L3 (central object store)
}

// Stats returns the cumulative resolution counters since the client was
// created. Used by the performance benchmark to prove cache promotion
// (job 1: L3->L2->L1, jobs 2-10: L1 hit) and to report hits/misses.
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

// Get returns asset bytes for hash, resolving L1 -> L2 -> L3 and promoting
// on each miss so the next lookup is faster.
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
