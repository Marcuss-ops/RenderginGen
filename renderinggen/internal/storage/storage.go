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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	// L2Corrupt counts the subset of L2ReadErrors caused by permission/I-O
	// degradation or non-regular entries (EACCES/corruption), as opposed to
	// any other non-absent failure. A miss is normal; a corrupt L2 is not.
	L2Corrupt int64
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
		L2Corrupt:    c.l2.corruptErrors.Load(),
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
	// L3 GET is deliberately a cheap existence probe (the object store does
	// not re-hash on every read). That makes this the boundary that must
	// verify: otherwise bytes from a corrupt/restored/malicious backend would
	// be promoted to L1/L2 and served under an address they do not have.
	if err := verifyContentAddress(hash, data); err != nil {
		log.Printf("storage: L3 fetch for %s returned bytes that do not match their address: %v", hash, err)
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
	// A declared size larger than the whole L2 budget is a cache-admission
	// answer, not a download failure: stream it once to an uncached temp file
	// so the caller still gets valid bytes for this job without installing an
	// entry the budget would evict immediately.
	if err := c.l2.admissible(size); err != nil {
		path, actual, streamErr := c.localPathUncached(hash, reader, size)
		if closeErr := reader.Close(); streamErr == nil {
			streamErr = closeErr
		}
		if streamErr != nil {
			c.finishPathFetch(hash, call, "", 0, streamErr)
			return "", 0, streamErr
		}
		c.l3Fetches.Add(1)
		c.finishPathFetch(hash, call, path, actual, nil)
		return path, actual, nil
	}
	path, actual, installErr := c.l2.InstallReader(hash, reader, size)
	reader.Close()
	if errors.Is(installErr, errL2ObjectTooLarge) {
		// The stream declared no size (or understated it) and the bytes did not
		// fit. The reader is consumed, so fetch once more and stream it outside
		// the cache rather than failing a job the L3 store can serve.
		retry, retrySize, fetchErr := backend.FetchReader(ctx, hash)
		if fetchErr != nil {
			c.finishPathFetch(hash, call, "", 0, fetchErr)
			return "", 0, fetchErr
		}
		path, actual, installErr = c.localPathUncached(hash, retry, retrySize)
		retry.Close()
	}
	if installErr != nil {
		c.finishPathFetch(hash, call, "", 0, installErr)
		return "", 0, installErr
	}
	c.l3Fetches.Add(1)
	c.finishPathFetch(hash, call, path, actual, nil)
	return path, actual, nil
}

// localPathUncached streams an L3 object to a temp file outside the cache,
// hashing it on the way. It is the LocalPath fallback for an object the L2
// budget cannot hold: the caller still receives a verified local file, and the
// cache never reports a path it is about to evict.
func (c *Client) localPathUncached(hash string, r io.Reader, size int64) (string, int64, error) {
	file, err := os.CreateTemp("", "renderinggen-uncached-*")
	if err != nil {
		return "", 0, err
	}
	path := file.Name()
	cleanup := func() {
		file.Close()
		os.Remove(path)
	}
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, digest), r)
	if err != nil {
		cleanup()
		return "", 0, err
	}
	if size >= 0 && written != size {
		cleanup()
		return "", 0, fmt.Errorf("L3 size %d does not match expected %d", written, size)
	}
	if isContentAddressName(hash) {
		if got := hex.EncodeToString(digest.Sum(nil)); got != hash {
			cleanup()
			return "", 0, fmt.Errorf("%w: L3 bytes hash to %s, not to key %s", ErrContentAddressMismatch, got, hash)
		}
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", 0, err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", 0, err
	}
	return path, written, nil
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
	// Verify before the store call: a backend that does not verify (the local
	// Memory backend, or a future adapter) must not receive, and the caches must
	// not adopt, bytes that contradict the address the caller named.
	if err := verifyContentAddress(hash, data); err != nil {
		return err
	}
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
		if errors.Is(err, errL2ObjectTooLarge) {
			// The object cannot fit the cache by configuration. L3 already holds
			// it; counting this as a write failure would make a legitimate budget
			// look like a broken disk.
			return
		}
		c.l2PutErrors.Add(1)
		log.Printf("storage: L2 cache write failed for %s: %v", hash, err)
	}
}

// warmL2File is the file-backed twin of warmL2. L3 already holds the artifact,
// so an L2 copy failure degrades performance and is counted, never a reason to
// fail publication. An oversize object is a cache-admission answer, not an
// error.
func (c *Client) warmL2File(hash, source string) {
	if c.l2 == nil || c.l2.dir == "" {
		return
	}
	if _, _, err := c.l2.PutFile(hash, source); err != nil {
		if errors.Is(err, errL2ObjectTooLarge) {
			return
		}
		c.l2PutErrors.Add(1)
		log.Printf("storage: L2 cache write failed for %s: %v (L3 is authoritative; cache degraded)", hash, err)
	}
}

// PutReader stores a large object without buffering it in the client. The
// optional ReaderBackend path is fully streaming; older backends retain
// compatibility through the existing byte-slice API. The streaming store is
// responsible for verifying the address (the HTTP store does); a caller that
// has a file should use PutFile to get the local pre-check as well.
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
		if err != nil {
			return "", 0, err
		}
		if err := verifyContentAddress(hash, data); err != nil {
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
		// Flush before handing the path out: callers treat the returned path
		// as durable (E4), so page-cache-only bytes must not be reported good.
		if err := file.Sync(); err != nil {
			file.Close()
			os.Remove(path)
			return "", 0, err
		}
		if err := file.Close(); err != nil {
			os.Remove(path)
			return "", 0, err
		}
		// Verify-read: an empty/short write must never be reported durable.
		if info, serr := os.Stat(path); serr != nil || info.Size() != int64(len(data)) {
			os.Remove(path)
			return "", 0, fmt.Errorf("storage: local-path verify failed for %s", path)
		}
		return path, int64(len(data)), nil
	}
	if path, size, err := c.l2.ContextPath(ctx, hash); err == nil {
		c.l2Hits.Add(1)
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
	if errors.Is(err, errL2ObjectTooLarge) {
		return c.localPathUncached(hash, bytes.NewReader(data), int64(len(data)))
	}
	if err != nil {
		return "", 0, err
	}
	return path, actual, nil
}

// PutFile stores a local file in L3 and warms L2. The file is hashed from the
// same open descriptor that is uploaded, before the store call: a caller that
// names the wrong address must not put those bytes into L3 at all, and the
// extra read is the price of that guarantee. L2 warming remains best-effort —
// L3 is authoritative, so a cache I/O failure only degrades the local tier.
func (c *Client) PutFile(ctx context.Context, hash, source string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	if isContentAddressName(hash) {
		digest := sha256.New()
		if _, err := io.Copy(digest, input); err != nil {
			_ = input.Close()
			return err
		}
		if got := hex.EncodeToString(digest.Sum(nil)); got != hash {
			_ = input.Close()
			return fmt.Errorf("%w: key %s, content %s", ErrContentAddressMismatch, hash, got)
		}
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			_ = input.Close()
			return err
		}
	}
	info, err := input.Stat()
	if err != nil {
		_ = input.Close()
		return err
	}
	if err := c.PutReader(ctx, hash, input, info.Size()); err != nil {
		_ = input.Close()
		return err
	}
	if err := input.Close(); err != nil {
		return err
	}
	c.warmL2File(hash, source)
	return nil
}

// Hash returns the content hash used as the cache key.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// verifyContentAddress checks the promised identity of data. It applies only
// to canonical SHA-256 keys: legacy development fixtures may use symbolic keys,
// which are not content addresses and cannot be checked this way.
func verifyContentAddress(key string, data []byte) error {
	if !isContentAddressName(key) {
		return nil
	}
	if got := Hash(data); got != key {
		return fmt.Errorf("%w: key %s, content %s", ErrContentAddressMismatch, key, got)
	}
	return nil
}
