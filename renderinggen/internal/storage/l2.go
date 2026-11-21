// l2.go owns the L2 disk cache contract: the read path (get/path/verification),
// the corruption handling and the index reconciliation. The on-disk model is
// in l2_cache_types.go and the write path in l2_write.go.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// WITHOUT the lock, so concurrent installs of different assets proceed in
// parallel instead of serializing behind whichever goroutine is copying the
// biggest file.
type diskCache struct {
	dir     string
	max     int64
	mu      sync.Mutex
	entries map[string]cacheEntry
	total   int64

	// reconcileOnce runs the startup index reconciliation at most once per
	// process (see ensureIndexed). It is separate from mu because the census
	// does filesystem I/O: holding the index lock across a walk of a large
	// cache tree would stall every concurrent reader for its duration.
	reconcileOnce sync.Once

	// readErrors counts L2 lookups that failed for a reason OTHER than
	// "absent". A cache miss is normal; an EACCES/EIO/unmounted directory is a
	// degradation that used to be indistinguishable from a miss (no counter,
	// no log), so a broken L2 could silently turn every job into an L3 fetch
	// while the hit-rate dashboards just showed misses.
	readErrors      atomic.Int64
	corruptErrors   atomic.Int64 // EACCES/EIO/non-regular entries: degradation, not a miss
	lastErrLog      atomic.Int64 // unix nanos of the last degradation log (rate limit)
	lastEvictErrLog atomic.Int64 // unix nanos of the last eviction-failure log (rate limit)

	// verified records the file identity (size + mtime) of content-addressed
	// entries whose bytes were hashed and matched their key. A cache hit is
	// only evidence that a file exists; the key is a SHA-256, so the bytes must
	// be proven to still hash to it before they are served or promoted to L1.
	// The stamp makes that proof once per process and re-proves it whenever the
	// file changes.
	verified map[string]cacheStamp

	// verifyMu guards verifying, the in-flight hashes. Two readers of the same
	// cold object share one hash pass; the verify itself runs without d.mu so a
	// large first read does not block unrelated cache traffic.
	verifyMu  sync.Mutex
	verifying map[string]*verifyCall
}

// reportReadError counts a non-"absent" L2 failure and logs it at most once
// per second: a broken cache directory must be visible in the logs and in the
// counters without turning every asset resolution into a log storm.
// Permission/I-O failures (EACCES/EIO) and non-regular entries are
// degradation/corruption, not ordinary misses: they bump the dedicated
// corrupt counter as well and log with the full path so an operator can tell
// "absent, fetch from L3" apart from "present but unreadable".
func (d *diskCache) reportReadError(op, key string, err error) {
	d.readErrors.Add(1)
	if isCacheCorrupt(err) {
		d.corruptErrors.Add(1)
	}
	now := time.Now().UnixNano()
	last := d.lastErrLog.Load()
	if now-last >= int64(time.Second) && d.lastErrLog.CompareAndSwap(last, now) {
		log.Printf("storage: L2 %s %s: %v (treated as a cache miss; L3 remains the source of truth)", op, d.path(key), err)
	}
}

// isCacheCorrupt reports whether err is a permission/I-O degradation rather
// than a plain miss. os.IsNotExist is handled by callers; anything matching
// permission or low-level I/O (EACCES/EPERM/EIO, including wrapped
// *os.PathError / *fs.PathError) is corruption/degradation.
func isCacheCorrupt(err error) bool {
	if err == nil {
		return false
	}
	if os.IsPermission(err) {
		return true
	}
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		s := strings.ToLower(pathErr.Err.Error())
		if strings.Contains(s, "permission") || strings.Contains(s, "input/output") || strings.Contains(s, "i/o") {
			return true
		}
	}
	return false
}

func (d *diskCache) Get(key string) ([]byte, bool) {
	// Hard failure path is byte-slice based: callers (storage.Client.Get)
	// already have the streaming LocalPath path for media. Loading a
	// multi-GB artifact via os.ReadFile here would spike heap. Guard by
	// size: large objects are treated as miss so the caller falls through to
	// the streaming LocalPath/ReaderBackend path instead of OOMing.
	p := d.path(key)
	info, err := os.Stat(p)
	if err != nil {
		if !os.IsNotExist(err) {
			d.reportReadError("stat", key, err)
		}
		return nil, false
	}
	const maxBytePathBytes = 64 << 20 // 64 MiB: beyond this use streaming LocalPath
	if info.Size() > maxBytePathBytes {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if !os.IsNotExist(err) {
			d.reportReadError("read", key, err)
		}
		return nil, false
	}
	// A hit is not trusted until the bytes are proven to hash to the key they
	// are stored under. This is the read-side half of the L2 contract: a file
	// modified after install (bit rot, an operator, a partial restore) must be
	// a miss, never bytes promoted to L1 under the wrong identity.
	if !d.contentVerified(key, p, info, func() (string, error) {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]), nil
	}) {
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
	if err != nil {
		if !os.IsNotExist(err) {
			d.reportReadError("stat", key, err)
		}
		return "", false
	}
	if !info.Mode().IsRegular() {
		// Present but not a regular file: corruption, not a miss. Counted on
		// the dedicated corrupt counter and logged with the path.
		d.corruptErrors.Add(1)
		d.readErrors.Add(1)
		log.Printf("storage: L2 path %s: not a regular file (treated as a cache miss; L3 remains the source of truth)", p)
		return "", false
	}
	// The streaming path hands out a path (which callers hard-link into a
	// workspace), so it must prove the bytes before returning it too. The proof
	// is paid once per file identity and then carried by verified.
	if !d.contentVerified(key, p, info, func() (string, error) { return hashFileContent(p) }) {
		return "", false
	}
	return p, true
}

// hashFileContent returns the hex SHA-256 of a cache file.
func hashFileContent(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// contentVerified proves that the bytes at p hash to key, remembering the file
// identity it proved so later hits skip the hash. hash is the caller's chosen
// hashing strategy: Get already has the bytes in memory, while Path must read
// the file.
//
// Concurrent cold readers of one object share a single hash pass. The pass runs
// outside d.mu (it can read a multi-GB file); on failure the corrupt entry is
// forgotten and, when still the same file, removed, so the caller's L3 refetch
// installs known-good bytes instead of repeatedly reading corrupted ones.
func (d *diskCache) contentVerified(key, p string, info os.FileInfo, hash func() (string, error)) bool {
	if !isContentAddressName(key) {
		return true
	}
	stamp := cacheStamp{size: info.Size(), modTime: info.ModTime()}
	d.mu.Lock()
	prev, ok := d.verified[key]
	d.mu.Unlock()
	if ok && prev == stamp {
		return true
	}

	id := fmt.Sprintf("%s|%d|%d", key, stamp.size, stamp.modTime.UnixNano())
	d.verifyMu.Lock()
	if call, ok := d.verifying[id]; ok {
		d.verifyMu.Unlock()
		<-call.done
		return call.ok
	}
	call := &verifyCall{done: make(chan struct{})}
	d.verifying[id] = call
	d.verifyMu.Unlock()

	got, hashErr := hash()
	verified := hashErr == nil && got == key
	if verified {
		d.mu.Lock()
		d.verified[key] = stamp
		d.mu.Unlock()
	} else if hashErr == nil {
		d.reportCorrupt("verify", key, fmt.Errorf("cached bytes hash to %s, not to their key %s", got, key))
		d.dropCorruptObject(key, p, info)
	} else {
		d.reportReadError("verify", key, hashErr)
	}

	d.verifyMu.Lock()
	call.ok = verified
	delete(d.verifying, id)
	close(call.done)
	d.verifyMu.Unlock()
	return verified
}

// reportCorrupt counts an integrity failure separately from an ordinary miss
// and logs it at most once per second, like reportReadError.
func (d *diskCache) reportCorrupt(op, key string, err error) {
	d.readErrors.Add(1)
	d.corruptErrors.Add(1)
	now := time.Now().UnixNano()
	last := d.lastErrLog.Load()
	if now-last >= int64(time.Second) && d.lastErrLog.CompareAndSwap(last, now) {
		log.Printf("storage: L2 %s %s: %v (corrupt entry treated as a cache miss; L3 remains the source of truth)", op, d.path(key), err)
	}
}

// dropCorruptObject forgets a corrupt entry and removes its file when it is
// still the same file that was hashed. A concurrent good install may have
// replaced it already; deleting that would throw away valid bytes.
func (d *diskCache) dropCorruptObject(key, p string, info os.FileInfo) {
	if current, err := os.Stat(p); err == nil && os.SameFile(info, current) {
		if rmErr := os.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("storage: L2 could not remove corrupt entry %s: %v", p, rmErr)
		}
	}
	d.mu.Lock()
	if entry, ok := d.entries[key]; ok {
		delete(d.entries, key)
		d.total -= entry.size
	}
	delete(d.verified, key)
	d.mu.Unlock()
}

// ensureIndexed runs the startup reconciliation at most once per process, and
// only when a budget exists to enforce. It is called before the first eviction
// decision, so the budget is measured against the bytes that are actually on
// disk rather than against the bytes this process happened to install.
func (d *diskCache) ensureIndexed() {
	if d == nil || d.dir == "" || d.max <= 0 {
		return
	}
	d.reconcileOnce.Do(d.reconcile)
}

// reconcile indexes the objects already present under the cache root.
//
// The index is a cache OF THE FILESYSTEM (see install), and that sentence is
// also why this pass has to exist: the previous process's index died with it,
// so after a restart d.total started at zero and enforceBudget could only
// reclaim what THIS process had installed. Every object left on disk by an
// earlier run was invisible to the budget — uncounted, therefore unevictable —
// and the cache directory grew without bound across restarts until the
// filesystem filled, at which point every install failed, every asset
// resolution degraded to an L3 fetch and the L2 tier silently stopped doing its
// job (the very failure mode l2PutErrors/reportReadError exist to surface).
//
// It is strictly best-effort: an unreadable directory is logged and the install
// proceeds, because a broken cache is a performance problem and never a
// correctness one — L3 stays the source of truth.
func (d *diskCache) reconcile() {
	root := filepath.Join(d.dir, "assets")
	type found struct {
		key      string
		size     int64
		accessed time.Time
	}
	var files []found
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil // no cache tree yet: nothing to index
			}
			return walkErr
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), stagingNamePrefix) {
			return nil
		}
		name := entry.Name()
		// Only the writer's own layout is indexed: <root>/<2-char prefix>/<key>,
		// exactly what d.path(key) reconstructs. A foreign file placed anywhere
		// else cannot be addressed through a key, so indexing it would make
		// eviction try to unlink a path that never held those bytes.
		if len(name) < 3 || filepath.Base(filepath.Dir(path)) != name[:2] {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil // unreadable or not an object: treated as absent, like a miss
		}
		files = append(files, found{key: name, size: info.Size(), accessed: info.ModTime()})
		return nil
	})
	if err != nil {
		log.Printf("storage: L2 index reconciliation under %s was incomplete: %v (the budget may under-report disk usage)", root, err)
	}
	if len(files) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var added int64
	indexed := 0
	for _, file := range files {
		if _, ok := d.entries[file.key]; ok {
			continue // already indexed by this process (a read or install won the race)
		}
		d.entries[file.key] = cacheEntry{size: file.size, accessed: file.accessed}
		d.total += file.size
		added += file.size
		indexed++
	}
	log.Printf("storage: L2 index reconciled from disk: %d object(s), %d byte(s) now accounted under %s", indexed, added, root)
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

func (d *diskCache) path(key string) string {
	prefix := key
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(d.dir, "assets", prefix, key)
}
