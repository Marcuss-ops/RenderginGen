package storage

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
)

// l2_write.go owns the write path: staging installs, the byte/file/reader
// variants, the budget eviction and the parent-directory sync every durable
// write depends on. Reads and corruption handling stay in l2.go.

// syncParentDir fsyncs a directory best-effort so a just-renamed entry is
// durable. Failures are logged, never fatal: on tmpfs there is nothing to
// persist and the rename is already visible.
func syncParentDir(dir string) {
	dfd, err := os.Open(dir)
	if err != nil {
		return
	}
	if err := dfd.Sync(); err != nil {
		log.Printf("storage: sync L2 parent directory %s: %v", dir, err)
	}
	if err := dfd.Close(); err != nil {
		log.Printf("storage: close L2 parent directory %s: %v", dir, err)
	}
}

// reportEvictionFailure logs an unremovable cache object at most once per
// second. The entry stays indexed and accounted: deleting it would make d.total
// under-report the bytes still on disk and hide the failure instead of
// surfacing it.
func (d *diskCache) reportEvictionFailure(path string, err error) {
	now := time.Now().UnixNano()
	last := d.lastEvictErrLog.Load()
	if now-last >= int64(time.Second) && d.lastEvictErrLog.CompareAndSwap(last, now) {
		log.Printf("storage: L2 eviction could not remove %s: %v (entry kept accounted; budget may remain above target)", path, err)
	}
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
	// The budget must be measured against what is already on disk before the
	// first eviction decision: a restarted worker otherwise sees an empty index,
	// believes the cache is empty and never reclaims a byte of the previous
	// run's objects.
	d.ensureIndexed()
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), stagingNamePrefix+"*")
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
	// Admission before commit: an object larger than the whole budget can never
	// stay in the cache, so installing it would only hand the caller a path that
	// the eviction pass (or the next one) deletes. Refuse it instead.
	if err := d.admissible(size); err != nil {
		return "", 0, err
	}
	// Durability: flush file pages before the atomic rename, then best-effort
	// dir-sync so the rename itself survives a crash/power-loss. Without Sync
	// the ledger can record an artifact as durable while its bytes are still
	// only in the page cache.
	if err := tmp.Sync(); err != nil {
		return "", 0, fmt.Errorf("L2 sync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	syncParentDir(filepath.Dir(p))

	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.entries[key]; ok {
		// The index is a cache OF THE FILESYSTEM, not an authority: the file can
		// vanish while the process keeps running (external cleanup, a tmpfs
		// reclaim, an operator deleting the cache). Trusting the entry blindly
		// returned a path with no bytes behind it, which the workspace then
		// tried to hard-link — a job failure that repeats for that content
		// address until the worker restarts. Verify before short-circuiting.
		if _, statErr := os.Stat(p); statErr == nil {
			// A concurrent install won the race for the same content address;
			// the bytes are identical by construction (the key is the hash).
			d.entries[key] = cacheEntry{size: prev.size, accessed: time.Now()}
			return p, prev.size, nil
		}
		delete(d.entries, key)
		delete(d.verified, key)
		d.total -= prev.size
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return "", 0, err
	}
	committed = true
	d.entries[key] = cacheEntry{size: size, accessed: time.Now()}
	d.total += size
	if d.max > 0 {
		// The install that triggered the pass must survive it. Without the
		// protection a timestamp tie could make the just-committed object the
		// oldest candidate, and install would return the path of its own
		// victim — the exact invalid-path contract admission is here to close.
		d.enforceBudget(key)
	}
	return p, size, nil
}

func (d *diskCache) PutBytes(key string, data []byte) (string, int64, error) {
	if err := d.admissible(int64(len(data))); err != nil {
		return "", 0, err
	}
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
	if err := d.admissible(info.Size()); err != nil {
		return "", 0, err
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
	if size >= 0 {
		if err := d.admissible(size); err != nil {
			return "", 0, err
		}
	}
	return d.install(key, func(tmp *os.File) (int64, error) {
		digest, written, err := hashio.Copy(r, tmp)
		if err != nil {
			return 0, err
		}
		if size >= 0 && written != size {
			return 0, fmt.Errorf("L3 size %d does not match expected %d", written, size)
		}
		if isContentAddressName(key) && digest != key {
			return 0, fmt.Errorf("%w: L3 hash %s does not match key %s", ErrContentAddressMismatch, digest, key)
		}
		return written, nil
	})
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

// enforceBudget evicts the oldest indexed entries. It never walks the
// filesystem itself: the objects an earlier process left on disk are indexed
// once by reconcile (see ensureIndexed, run before the first budget decision),
// which is what keeps this an O(entries) metadata pass instead of a per-write
// directory scan. The caller holds d.mu; only metadata operations (unlink)
// happen here.
//
// The selection is ONE pass over the index that keeps the evictionBatch oldest
// entries in a bounded, sorted window, instead of re-scanning the whole index
// once per victim. The historical shape was O(evictionBatch x entries) map
// iterations under d.mu (up to 64 full scans per over-budget install, which
// serialized every concurrent L2 writer); this is O(entries + batch log batch)
// with identical eviction order and identical accounting.
func (d *diskCache) enforceBudget(protect string) {
	if d.total <= d.max {
		return
	}
	// Bounded oldest-first window (insertion-sorted, allocation-free).
	type candidate struct {
		key      string
		accessed time.Time
	}
	oldest := make([]candidate, 0, evictionBatch)
	for key, entry := range d.entries {
		if key == protect {
			continue // never evict the object this pass was triggered by
		}
		if len(oldest) == evictionBatch && !entry.accessed.Before(oldest[len(oldest)-1].accessed) {
			continue // not older than the current window's newest member
		}
		var slot int
		if len(oldest) == evictionBatch {
			slot = len(oldest) - 1
		} else {
			oldest = append(oldest, candidate{})
			slot = len(oldest) - 1
		}
		for slot > 0 && oldest[slot-1].accessed.After(entry.accessed) {
			oldest[slot] = oldest[slot-1]
			slot--
		}
		oldest[slot] = candidate{key: key, accessed: entry.accessed}
	}

	for _, victim := range oldest {
		if d.total <= d.max {
			return
		}
		if victim.key == protect {
			continue
		}
		entry, ok := d.entries[victim.key]
		if !ok {
			continue // already removed earlier in this pass
		}
		err := os.Remove(d.path(victim.key))
		switch {
		case err == nil || os.IsNotExist(err):
			// The bytes are gone from the cache either way: account them. A
			// vanished file (external cleanup) must not leave a stale index
			// entry that every future pass re-scans forever.
			delete(d.entries, victim.key)
			delete(d.verified, victim.key)
			d.total -= entry.size
		default:
			// Unremovable file (held open by a renderer, read-only directory):
			// the bytes are still there and still occupy disk. Keep the entry
			// indexed and accounted, then continue with the next candidate.
			// Forgetting it would under-report disk usage forever and make the
			// budget unrecoverable. If every candidate is unremovable, d.total
			// truthfully stays above max and the next pass retries.
			d.reportEvictionFailure(d.path(victim.key), err)
		}
	}
}
