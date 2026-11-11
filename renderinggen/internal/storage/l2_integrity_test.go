package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestL2RefusesObjectLargerThanBudget pins the fix for the self-evicting
// install: an object larger than the whole cache used to be written, indexed,
// and then deleted by enforceBudget in the same call, while install returned
// the path of the file it had just removed. Admission must answer before the
// commit and must not leave bytes behind.
func TestL2RefusesObjectLargerThanBudget(t *testing.T) {
	d := newDiskCache(t.TempDir(), 10)
	payload := bytes.Repeat([]byte("x"), 11)
	key := Hash(payload)

	path, size, err := d.PutBytes(key, payload)
	if !errors.Is(err, errL2ObjectTooLarge) {
		t.Fatalf("PutBytes error = %v, want errL2ObjectTooLarge", err)
	}
	if path != "" || size != 0 {
		t.Fatalf("refused install returned path=%q size=%d, want no path", path, size)
	}
	if _, statErr := os.Stat(d.path(key)); !os.IsNotExist(statErr) {
		t.Fatalf("oversized object was written to the cache (stat err = %v)", statErr)
	}
	if d.total != 0 {
		t.Fatalf("budget accounting = %d after refused install, want 0", d.total)
	}

	// An object exactly equal to the budget is admissible and survives its own
	// install (the pathological case is strictly larger, not equal).
	exact := bytes.Repeat([]byte("e"), 10)
	exactKey := Hash(exact)
	exactPath, exactSize, err := d.PutBytes(exactKey, exact)
	if err != nil {
		t.Fatalf("exact-fit install: %v", err)
	}
	if exactSize != int64(len(exact)) {
		t.Fatalf("exact-fit size = %d, want %d", exactSize, len(exact))
	}
	if _, statErr := os.Stat(exactPath); statErr != nil {
		t.Fatalf("exact-fit object was not left on disk: %v", statErr)
	}
}

// TestL2BudgetNeverEvictsTheFreshInstall pins the second half of the admission
// contract: the install whose commit triggers enforceBudget must never be the
// pass's victim. Admission refuses objects larger than the budget, but a
// timestamp tie among same-sized entries could still select the fresh object as
// "oldest"; install would then return a path it had just unlinked.
func TestL2BudgetNeverEvictsTheFreshInstall(t *testing.T) {
	d := newDiskCache(t.TempDir(), 10)
	oldBytes := bytes.Repeat([]byte("a"), 5)
	freshBytes := bytes.Repeat([]byte("b"), 5)
	oldKey, freshKey := Hash(oldBytes), Hash(freshBytes)
	if _, _, err := d.PutBytes(oldKey, oldBytes); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PutBytes(freshKey, freshBytes); err != nil {
		t.Fatal(err)
	}

	// Make the fresh entry look oldest and put the cache over budget, as a
	// third (already-removed) entry would have done.
	d.mu.Lock()
	d.entries[freshKey] = cacheEntry{size: int64(len(freshBytes)), accessed: time.Now().Add(-time.Hour)}
	d.entries[oldKey] = cacheEntry{size: int64(len(oldBytes)), accessed: time.Now()}
	d.total = int64(len(oldBytes) + len(freshBytes) + 5)
	d.mu.Unlock()

	d.enforceBudget(freshKey)
	if _, err := os.Stat(d.path(freshKey)); err != nil {
		t.Fatalf("the install that triggered eviction was removed: %v", err)
	}
	if _, err := os.Stat(d.path(oldKey)); !os.IsNotExist(err) {
		t.Fatalf("an older unprotected entry survived (stat err = %v)", err)
	}
}

// TestL2RejectsTamperedCacheEntry pins the read-side half of the L2 contract.
// A cache hit is only evidence that a FILE exists; when the key is a SHA-256,
// serving bytes that no longer hash to it is the same corruption as accepting
// a bad write. The first read proves the object and remembers the proof; a
// later modification changes the file stamp and forces a new proof, which must
// fail closed and drop the entry so L3 can reinstall it.
func TestL2RejectsTamperedCacheEntry(t *testing.T) {
	d := newDiskCache(t.TempDir(), 0)
	payload := []byte("trusted-cache-bytes")
	key := Hash(payload)
	if _, _, err := d.PutBytes(key, payload); err != nil {
		t.Fatal(err)
	}
	if data, ok := d.Get(key); !ok || !bytes.Equal(data, payload) {
		t.Fatalf("fresh entry did not read back: ok=%v data=%q", ok, data)
	}

	path := d.path(key)
	// Same length, different bytes: a size-only check would not notice.
	if err := os.WriteFile(path, []byte("corrupt-cache-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Make the change detectable even on a filesystem with coarse timestamps.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if data, ok := d.Get(key); ok {
		t.Fatalf("tampered L2 entry was served as valid: %q", data)
	}
	if _, ok := d.Path(key); ok {
		t.Fatal("tampered L2 entry still yields a path")
	}

	// The corrupt file must be gone, so the next L3 fetch installs clean bytes
	// instead of tripping over the same entry forever.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt cache file was not removed (stat err = %v)", err)
	}
	// The budget must forget those bytes too: a dropped corrupt entry that still
	// counted against d.total would leave the cache permanently over budget.
	if d.total != 0 {
		t.Fatalf("budget accounting = %d after dropping a corrupt entry, want 0", d.total)
	}
	d.mu.Lock()
	entries := len(d.entries)
	d.mu.Unlock()
	if entries != 0 {
		t.Fatalf("index still holds %d entries after dropping a corrupt entry", entries)
	}
}

// TestL2PathVerifiesBeforeHandingOutPath covers the streaming entry point:
// LocalPath hands the path to a workspace that hard-links it, so a path hit
// must be proven too, not only the byte-slice Get.
func TestL2PathVerifiesBeforeHandingOutPath(t *testing.T) {
	d := newDiskCache(t.TempDir(), 0)
	payload := []byte("streamed-cache-bytes")
	key := Hash(payload)
	if _, _, err := d.PutBytes(key, payload); err != nil {
		t.Fatal(err)
	}
	path := d.path(key)
	if err := os.WriteFile(path, []byte("streamed-CACHE-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Path(key); ok {
		t.Fatal("Path returned a tampered object")
	}
}

// TestLocalPathServesOversizeObjectWithoutCaching pins the caller-facing
// consequence of admission: an asset larger than the L2 budget still resolves
// (from L3, to a temp file) instead of failing the job, and no cache entry is
// created only to be evicted.
func TestLocalPathServesOversizeObjectWithoutCaching(t *testing.T) {
	ctx := context.Background()
	payload := bytes.Repeat([]byte("o"), 4096)
	h := Hash(payload)
	backend := &readerCountingBackend{inner: NewMemory()}
	if err := backend.Store(ctx, h, payload); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: dir, L2MaxBytes: 64})

	path, size, err := c.LocalPath(ctx, h)
	if err != nil {
		t.Fatalf("LocalPath for oversize object: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })
	if size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read resolved path: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("resolved bytes differ from the object")
	}
	if _, err := os.Stat(c.l2.path(h)); !os.IsNotExist(err) {
		t.Fatalf("oversize object was installed in L2 (stat err = %v)", err)
	}
}

// TestL2EvictionFailureKeepsAccountingAndEvictsNextEntry pins the failure
// branch of the budget pass. An object that cannot be unlinked still occupies
// disk: dropping its index entry and subtracting its bytes would make d.total
// lie and, worse, stop the pass before evictable objects are reclaimed. The
// pass must keep the failed entry accounted, move on, and bound the cache with
// the entries it can remove.
func TestL2EvictionFailureKeepsAccountingAndEvictsNextEntry(t *testing.T) {
	d := newDiskCache(t.TempDir(), 10)
	blocked := strings.Repeat("a", 64)
	removable := strings.Repeat("b", 64)
	fresh := strings.Repeat("c", 64)
	now := time.Now()

	// A non-empty directory at the object path makes os.Remove fail with
	// ENOTEMPTY, exercising the unremovable-file branch on every platform this
	// package targets (an empty directory would be removed successfully).
	blockedPath := d.path(blocked)
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{removable, fresh} {
		if err := os.MkdirAll(filepath.Dir(d.path(key)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d.path(key), []byte("12345"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	d.mu.Lock()
	d.entries[blocked] = cacheEntry{size: 5, accessed: now.Add(-3 * time.Hour)}
	d.entries[removable] = cacheEntry{size: 5, accessed: now.Add(-2 * time.Hour)}
	d.entries[fresh] = cacheEntry{size: 5, accessed: now}
	d.total = 15
	d.mu.Unlock()

	d.enforceBudget(fresh)

	d.mu.Lock()
	_, blockedIndexed := d.entries[blocked]
	_, removableIndexed := d.entries[removable]
	total := d.total
	d.mu.Unlock()
	if !blockedIndexed {
		t.Fatal("an unremovable entry was forgotten; d.total no longer describes the disk")
	}
	if removableIndexed {
		t.Fatal("the evictable entry was not reclaimed after the failed victim")
	}
	if total != 10 {
		t.Fatalf("budget accounting = %d, want 10 (blocked 5 + fresh 5)", total)
	}
	if _, err := os.Stat(blockedPath); err != nil {
		t.Fatalf("blocked directory disappeared: %v", err)
	}
	if _, err := os.Stat(d.path(removable)); !os.IsNotExist(err) {
		t.Fatalf("removable entry still on disk (stat err = %v)", err)
	}
	if _, err := os.Stat(d.path(fresh)); err != nil {
		t.Fatalf("protected fresh entry was removed: %v", err)
	}
}
