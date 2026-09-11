// Package storage implements the shared artifact store with a three-level cache.
//
//	L1  in-memory bytes (Go heap, worker lifetime, small objects only)
//	L2  local NVMe      (content-addressed on disk, page-cache backed)
//	L3  object store    (central, shared, persistent)
//
// Which tier serves a request depends on the entry point:
//
//	Get/Put        byte API: L1 first, then L2, then L3. Used by the small
//	               plan/intent uploads and by backends without streaming.
//	LocalPath      streaming API used by asset materialization: L2 (a path,
//	               zero-copy into the workspace), then L1 if the L2 file was
//	               evicted, then a streaming L3 fetch installed into L2.
//
// L2 is deliberately the primary cache for the streaming path: it hands back a
// path the workspace hard-links, while media on the Go heap would only add GC
// pressure. L1 therefore caches small, high-reuse objects and never media.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
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
	l1Hits         atomic.Int64
	l2Hits         atomic.Int64
	l3Fetches      atomic.Int64
	l2PutErrors    atomic.Int64 // L2 warm writes that failed (cache degraded)
	inflightMu     sync.Mutex
	inflight       map[string]*fetchCall // byte-path single-flight (Get)
	pathInflightMu sync.Mutex
	pathInflight   map[string]*pathFetchCall // streaming single-flight (LocalPath)
}

type fetchCall struct {
	done chan struct{}
	data []byte
	err  error
}

type pathFetchCall struct {
	done chan struct{}
	path string
	size int64
	err  error
}

// CacheStats is a point-in-time snapshot of the resolution counters.
type CacheStats struct {
	L1Hits      int64 // resolved from L1 (in-memory)
	L2Hits      int64 // resolved from L2 (on-disk)
	L3Fetches   int64 // resolved from L3 (central object store)
	L2PutErrors int64 // failed L2 warm writes (cache silently degraded if > 0)
	// L2ReadErrors counts L2 lookups that failed for a reason other than
	// "absent" (permissions, I/O, unmounted directory). The lookup degrades to
	// an L3 fetch, so a non-zero value here means the on-disk cache is not
	// doing its job even though the hit counters look like ordinary misses.
	L2ReadErrors int64
}

// Stats returns the cumulative resolution counters since the client was
// created. Used by the performance benchmark to prove cache promotion
// (job 1: L3->L2->L1, jobs 2-10: L1 hit) and to report hits/misses.
func (c *Client) Stats() CacheStats {
	return CacheStats{
		L1Hits:       c.l1Hits.Load(),
		L2Hits:       c.l2Hits.Load(),
		L3Fetches:    c.l3Fetches.Load(),
		L2PutErrors:  c.l2PutErrors.Load(),
		L2ReadErrors: c.l2.readErrors.Load(),
	}
}

// New creates a cache client over the given L3 backend.
func New(backend Backend, opts Options) *Client {
	return &Client{
		backend:      backend,
		l1:           newMemCache(opts.L1MaxBytes),
		l2:           newDiskCache(opts.L2Dir, opts.L2MaxBytes),
		inflight:     make(map[string]*fetchCall),
		pathInflight: make(map[string]*pathFetchCall),
	}
}

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
	c.warmL2(hash, data)
	c.l1.Put(hash, data)
	c.finishFetch(hash, call, data, nil)
	return data, nil
}

// localPathFetch resolves an L3 miss for LocalPath with single-flight
// semantics: several prep workers materializing jobs that share the same cold
// asset (a common background video) must issue ONE L3 download, not one per
// worker. The leader streams the object into L2; followers wait for the
// install and return the same durable path.
func (c *Client) localPathFetch(ctx context.Context, hash string, backend ReaderBackend) (string, int64, error) {
	call, leader := c.startPathFetch(hash)
	if !leader {
		select {
		case <-call.done:
			if call.err != nil {
				return "", 0, call.err
			}
			return call.path, call.size, nil
		case <-ctx.Done():
			return "", 0, ctx.Err()
		}
	}

	reader, size, err := backend.FetchReader(ctx, hash)
	if err != nil {
		c.finishPathFetch(hash, call, "", 0, err)
		return "", 0, err
	}
	path, actual, installErr := c.l2.InstallReader(hash, reader, size)
	reader.Close()
	if installErr != nil {
		c.finishPathFetch(hash, call, "", 0, installErr)
		return "", 0, installErr
	}
	c.l3Fetches.Add(1)
	c.finishPathFetch(hash, call, path, actual, nil)
	return path, actual, nil
}

func (c *Client) startPathFetch(hash string) (*pathFetchCall, bool) {
	c.pathInflightMu.Lock()
	defer c.pathInflightMu.Unlock()
	if call, ok := c.pathInflight[hash]; ok {
		return call, false
	}
	call := &pathFetchCall{done: make(chan struct{})}
	c.pathInflight[hash] = call
	return call, true
}

func (c *Client) finishPathFetch(hash string, call *pathFetchCall, path string, size int64, err error) {
	c.pathInflightMu.Lock()
	call.path = path
	call.size = size
	call.err = err
	delete(c.pathInflight, hash)
	close(call.done)
	c.pathInflightMu.Unlock()
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
	// Own the follower copy OUTSIDE the critical section: the copy cost is
	// proportional to the object size, and holding inflightMu across it made
	// every other hash's startFetch wait for the largest object in flight.
	var stored []byte
	if err == nil {
		stored = append([]byte(nil), data...)
	}
	c.inflightMu.Lock()
	call.data = stored
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
	c.warmL2(hash, data)
	c.l1.Put(hash, data)
	return nil
}

// warmL2 installs bytes into the L2 disk cache. L3 is already the source of
// truth, so a failed cache write degrades performance but not correctness —
// still, it is counted and logged so a dead/full L2 disk cannot silently turn
// every job into an L3 fetch.
func (c *Client) warmL2(hash string, data []byte) {
	if err := c.l2.Put(hash, data); err != nil {
		c.l2PutErrors.Add(1)
		log.Printf("storage: L2 cache write failed for %s: %v", hash, err)
	}
}

// PutReader stores a large object without buffering it in the client. The
// optional ReaderBackend path is fully streaming; older backends retain
// compatibility through the existing byte-slice API.
func (c *Client) PutReader(ctx context.Context, hash string, r io.Reader, size int64) error {
	if backend, ok := c.backend.(WriterBackend); ok {
		if err := backend.StoreReader(ctx, hash, r, size); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("storage backend does not support streaming writes")
}

// LocalPath returns a verified local L2 path, fetching and atomically installing
// the object into L2 when necessary.
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
	// L2 miss, L1 hit: the streaming materialize path used to bypass L1
	// entirely, so an object whose L2 file was evicted (or installed by the
	// byte API) was re-downloaded from L3 even though its bytes were still in
	// RAM. Installing them into L2 here is one write for one avoided L3 round
	// trip, and it makes the L1 hit counter describe the materialize path
	// too. This runs only after a confirmed L2 miss: the common case (L2 hit)
	// pays nothing.
	if data, ok := c.l1.Get(hash); ok {
		c.l1Hits.Add(1)
		path, size, err := c.l2.PutBytes(hash, data)
		if err == nil {
			return path, size, nil
		}
		c.l2PutErrors.Add(1)
		log.Printf("storage: L2 reinstall from L1 failed for %s: %v (falling back to L3)", hash, err)
	}
	if backend, ok := c.backend.(ReaderBackend); ok {
		return c.localPathFetch(ctx, hash, backend)
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

// PutFile stores a local file in L3 and installs the same bytes in L2.
func (c *Client) PutFile(ctx context.Context, hash, source string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	info, err := input.Stat()
	if err != nil {
		input.Close()
		return err
	}
	if err := c.PutReader(ctx, hash, input, info.Size()); err != nil {
		input.Close()
		return err
	}
	if err := input.Close(); err != nil {
		return err
	}
	_, _, err = c.l2.PutFile(hash, source)
	return err
}

// Hash returns the content hash used as the cache key.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
