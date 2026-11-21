package storage

import (
	"errors"
	"fmt"
	"time"
)

// l2_cache_types.go owns the on-disk L2 cache model: the directory, its stamps
// and entries, admissibility, the constructor and the naming/eviction
// constants. The read paths stay in l2.go, the write paths in l2_write.go.

// errL2ObjectTooLarge means an object cannot fit within the L2 budget. It is a
// cache-admission answer, not a storage failure: L3 remains the source of
// truth, and install must NOT write bytes it would immediately have to evict —
// returning the path of a file already deleted by the budget is worse than
// reporting that the object was never cached.
var errL2ObjectTooLarge = errors.New("object exceeds the L2 cache budget")

// diskCache is the L2 NVMe cache: content-addressed files on disk.
//
// The mutex guards only the in-memory index (entries/total) and the short
// commit phase (existence check + rename + eviction bookkeeping). All large
// I/O — streaming a fetched object to disk, hashing it, copying a file — runs
type cacheStamp struct {
	size    int64
	modTime time.Time
}

type verifyCall struct {
	done chan struct{}
	ok   bool
}

// isContentAddressName reports whether key is a canonical SHA-256 content
// address. Non-content keys (short test keys, legacy symbolic names) are not
// hash-verifiable and keep the historical path-existence behaviour.
func isContentAddressName(key string) bool {
	if len(key) != 64 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// admissible reports whether an object of size bytes may enter the cache. An
// object larger than the whole budget is never installed: enforceBudget could
// only delete it again (it is the newest entry, so it is also the only entry
// when the cache is otherwise empty), while install would still hand its path
// to the caller.
func (d *diskCache) admissible(size int64) error {
	if d.max > 0 && size > d.max {
		return fmt.Errorf("%w: %d bytes will not fit a %d-byte cache", errL2ObjectTooLarge, size, d.max)
	}
	return nil
}

type cacheEntry struct {
	size     int64
	accessed time.Time
}

func newDiskCache(dir string, max int64) *diskCache {
	return &diskCache{
		dir:       dir,
		max:       max,
		entries:   make(map[string]cacheEntry),
		verified:  make(map[string]cacheStamp),
		verifying: make(map[string]*verifyCall),
	}
}

// stagingNamePrefix is the name prefix install uses for its in-flight temp
// files. A leftover file with this prefix is an interrupted write, never an
// object: it is skipped by the startup census (reconcile) for the same reason
// it can never be addressed by a content key.
const stagingNamePrefix = ".install-"

// evictionBatch bounds the work of a single budget-enforcement pass. The
// historical implementation snapshotted the whole index and sorted it O(n log n)
// under d.mu on every over-budget install — with ~100k small entries one pass
// became a multi-millisecond stall of every concurrent cache user plus a full
// candidate-slice allocation in the path that is supposed to be cheap. Each
// pass now removes at most evictionBatch of the oldest entries via an
// allocation-free scan, so worst-case per-install stall is bounded. When one
// install overflows the budget by more than a batch can reclaim, subsequent
// installs finish the eviction (amortized); accounting stays exact.
const evictionBatch = 64
