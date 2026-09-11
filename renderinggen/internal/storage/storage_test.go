package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestGetResolvesL3AndPromotesToL2(t *testing.T) {
	ctx := context.Background()
	backend := NewMemory()
	h := Hash([]byte("hello"))
	if err := backend.Store(ctx, h, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: dir, L2MaxBytes: 1 << 20})

	data, err := c.Get(ctx, h)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("got %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "assets", h[:2], h)); err != nil {
		t.Fatalf("L2 file not written: %v", err)
	}
}

func TestL2CacheServesAcrossClients(t *testing.T) {
	ctx := context.Background()
	backend := NewMemory()
	h := Hash([]byte("hello"))
	if err := backend.Store(ctx, h, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	cb1 := &countingBackend{inner: backend}
	c1 := New(cb1, Options{L1MaxBytes: 1 << 20, L2Dir: dir})
	if _, err := c1.Get(ctx, h); err != nil {
		t.Fatal(err)
	}
	if cb1.calls != 1 {
		t.Fatalf("want 1 L3 call, got %d", cb1.calls)
	}

	// Fresh client (empty L1) sharing the same L2 dir should hit L2, not L3.
	cb2 := &countingBackend{inner: backend}
	c2 := New(cb2, Options{L1MaxBytes: 1 << 20, L2Dir: dir})
	data, err := c2.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if cb2.calls != 0 {
		t.Fatalf("want 0 L3 calls (L2 hit), got %d", cb2.calls)
	}
	if string(data) != "hello" {
		t.Fatalf("got %q", data)
	}
}

func TestStatsCountersPromotion(t *testing.T) {
	ctx := context.Background()
	backend := NewMemory()
	h := Hash([]byte("hello"))
	if err := backend.Store(ctx, h, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	cb := &countingBackend{inner: backend}
	c := New(cb, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir()})

	// First get: L3 miss -> L2+ promote, then L1.
	if _, err := c.Get(ctx, h); err != nil {
		t.Fatal(err)
	}
	s := c.Stats()
	if s.L3Fetches != 1 || s.L1Hits != 0 {
		t.Fatalf("after first get want L3Fetches=1 L1Hits=0, got %+v", s)
	}

	// Second get: served from L1.
	if _, err := c.Get(ctx, h); err != nil {
		t.Fatal(err)
	}
	s = c.Stats()
	if s.L3Fetches != 1 || s.L1Hits != 1 {
		t.Fatalf("after second get want L3Fetches=1 L1Hits=1, got %+v", s)
	}
	if cb.calls != 1 {
		t.Fatalf("want 1 L3 call total, got %d", cb.calls)
	}
}

func TestL1ServesRepeatedGets(t *testing.T) {
	ctx := context.Background()
	backend := NewMemory()
	h := Hash([]byte("hello"))
	if err := backend.Store(ctx, h, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	cb := &countingBackend{inner: backend}
	c := New(cb, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir()})

	if _, err := c.Get(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, h); err != nil {
		t.Fatal(err)
	}
	if cb.calls != 1 {
		t.Fatalf("want 1 L3 call (2nd get from L1), got %d", cb.calls)
	}
}

func TestPutWritesL3AndWarmsCaches(t *testing.T) {
	ctx := context.Background()
	backend := NewMemory()
	h := Hash([]byte("world"))
	c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir()})

	if err := c.Put(ctx, h, []byte("world")); err != nil {
		t.Fatal(err)
	}
	data, err := backend.Fetch(ctx, h)
	if err != nil || string(data) != "world" {
		t.Fatalf("L3 should hold the object: %v %q", err, data)
	}
}

func TestL1Eviction(t *testing.T) {
	m := newMemCache(10)
	m.Put("a", []byte("12345"))
	m.Put("b", []byte("12345"))
	m.Put("c", []byte("12345")) // evicts "a"

	if _, ok := m.Get("a"); ok {
		t.Fatal("a should be evicted")
	}
	if _, ok := m.Get("b"); !ok {
		t.Fatal("b should remain")
	}
	if _, ok := m.Get("c"); !ok {
		t.Fatal("c should remain")
	}
}

func TestHTTPBackendRoundTrip(t *testing.T) {
	ctx := context.Background()
	objects := map[string][]byte{}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /objects/{key}", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		objects[r.PathValue("key")] = body
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /objects/{key}", func(w http.ResponseWriter, r *http.Request) {
		data, ok := objects[r.PathValue("key")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := NewHTTP(ts.URL)
	if err := h.Store(ctx, "abc", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := h.Fetch(ctx, "abc")
	if err != nil || string(data) != "hello" {
		t.Fatalf("fetch: %v %q", err, data)
	}
	if _, err := h.Fetch(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

type countingBackend struct {
	inner Backend
	calls int
}

func (c *countingBackend) Fetch(ctx context.Context, key string) ([]byte, error) {
	c.calls++
	return c.inner.Fetch(ctx, key)
}

func (c *countingBackend) Store(ctx context.Context, key string, data []byte) error {
	return c.inner.Store(ctx, key, data)
}

func TestConcurrentInstallSameKey(t *testing.T) {
	payload := []byte("concurrent-install-payload-12345")
	h := Hash(payload)
	d := newDiskCache(t.TempDir(), 0)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, size, err := d.PutBytes(h, payload)
			if err != nil {
				t.Errorf("install: %v", err)
				return
			}
			if path == "" || size != int64(len(payload)) {
				t.Errorf("install returned path=%q size=%d", path, size)
			}
		}()
	}
	wg.Wait()

	data, ok := d.Get(h)
	if !ok {
		t.Fatal("object missing after concurrent installs")
	}
	if string(data) != string(payload) {
		t.Fatalf("corrupted payload after concurrent installs: %q", data)
	}
}

// readerCountingBackend counts streaming L3 fetches (FetchReader), which is
// the path LocalPath uses on a miss.
type readerCountingBackend struct {
	inner Backend
	calls atomic.Int64
}

func (b *readerCountingBackend) FetchReader(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	b.calls.Add(1)
	data, err := b.inner.Fetch(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (b *readerCountingBackend) Fetch(ctx context.Context, key string) ([]byte, error) {
	return b.inner.Fetch(ctx, key)
}

func (b *readerCountingBackend) Store(ctx context.Context, key string, data []byte) error {
	return b.inner.Store(ctx, key, data)
}

func TestLocalPathConcurrentMissDownloadsOnce(t *testing.T) {
	ctx := context.Background()
	payload := bytes.Repeat([]byte("x"), 1<<20) // 1 MiB: not L1-admissible, forces the reader path
	backend := &readerCountingBackend{inner: NewMemory()}
	h := Hash(payload)
	if err := backend.Store(ctx, h, payload); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir(), L2MaxBytes: 64 << 20})
	paths := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], _, errs[i] = c.LocalPath(ctx, h)
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if paths[i] != paths[0] {
			t.Fatalf("workers resolved different paths: %q vs %q", paths[i], paths[0])
		}
	}
	if got := backend.calls.Load(); got != 1 {
		t.Fatalf("concurrent cold LocalPath: want 1 L3 fetch, got %d", got)
	}
	if got := c.Stats().L3Fetches; got != 1 {
		t.Fatalf("L3Fetches counter: want 1, got %d", got)
	}
}

// TestLocalPathReinstallsFromL1WhenL2Evicted pins the fix that makes the L1
// tier participate in the streaming resolution path: materialization used to
// consult only L2, so an object whose on-disk copy was evicted was fetched
// from L3 again even though its bytes were still resident in RAM.
func TestLocalPathReinstallsFromL1WhenL2Evicted(t *testing.T) {
	ctx := context.Background()
	payload := []byte("l1-resident-payload")
	h := Hash(payload)
	backend := &countingBackend{inner: NewMemory()}
	dir := t.TempDir()
	c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: dir, L2MaxBytes: 1 << 20})

	// Put: L3 + L2 + L1 are all warm for this content address.
	if err := c.Put(ctx, h, payload); err != nil {
		t.Fatal(err)
	}
	l2Path := filepath.Join(dir, "assets", h[:2], h)
	if err := os.Remove(l2Path); err != nil {
		t.Fatalf("simulate L2 eviction: %v", err)
	}

	path, size, err := c.LocalPath(ctx, h)
	if err != nil {
		t.Fatalf("LocalPath after L2 eviction: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read resolved path: %v", err)
	}
	if !bytes.Equal(data, payload) || size != int64(len(payload)) {
		t.Fatalf("resolved bytes mismatch: size=%d data=%q", size, data)
	}
	stats := c.Stats()
	if stats.L1Hits != 1 {
		t.Fatalf("L1Hits = %d, want 1 (the install must be attributed to L1)", stats.L1Hits)
	}
	if backend.calls != 0 {
		t.Fatalf("L3 Fetch calls = %d, want 0 (L1 must satisfy an L2 miss)", backend.calls)
	}
}

// TestL2ReadErrorsAreCountedNotSwallowed pins the other half of the cache
// degradation contract: an L2 lookup that fails for a reason other than
// "absent" (here a symlink loop, which fails with ELOOP on every platform)
// must be counted instead of silently looking like an ordinary miss. The log
// line is rate-limited, the counter is not.
func TestL2ReadErrorsAreCountedNotSwallowed(t *testing.T) {
	payload := []byte("l2-read-error")
	h := Hash(payload)
	dir := t.TempDir()
	d := newDiskCache(dir, 0)
	if _, _, err := d.PutBytes(h, payload); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := d.path(h)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, p); err != nil {
		t.Skipf("cannot create a symlink loop in this environment: %v", err)
	}

	if _, ok := d.Get(h); ok {
		t.Fatal("Get succeeded on an unreadable L2 entry")
	}
	if got := d.readErrors.Load(); got != 1 {
		t.Fatalf("readErrors = %d after one unreadable Get, want 1", got)
	}
	if _, ok := d.Path(h); ok {
		t.Fatal("Path succeeded on an unreadable L2 entry")
	}
	if got := d.readErrors.Load(); got != 2 {
		t.Fatalf("readErrors = %d after an unreadable Path, want 2", got)
	}
}

// TestClientReportsL2ReadErrors proves the counter reaches the operator-facing
// snapshot: a caller that only watches the hit rates cannot tell a broken cache
// directory from a cold one, so the degradation must be lifted out of the
// cache in CacheStats.
func TestClientReportsL2ReadErrors(t *testing.T) {
	ctx := context.Background()
	payload := []byte("degraded-l2")
	h := Hash(payload)
	dir := t.TempDir()
	c := New(&countingBackend{inner: NewMemory()}, Options{L1MaxBytes: 1 << 20, L2Dir: dir})
	if err := c.Put(ctx, h, payload); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(dir, "assets", h[:2], h)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, p); err != nil {
		t.Skipf("cannot create a symlink loop in this environment: %v", err)
	}

	// The streaming path must still succeed (L3 is the source of truth) while
	// reporting that the cache level itself is broken.
	if _, _, err := c.LocalPath(ctx, h); err != nil {
		t.Fatalf("LocalPath must degrade to L3, got: %v", err)
	}
	if got := c.Stats().L2ReadErrors; got == 0 {
		t.Fatal("CacheStats.L2ReadErrors = 0, want the L2 degradation reported")
	}
}

func TestL2Eviction(t *testing.T) {
	dir := t.TempDir()
	d := newDiskCache(dir, 10) // 10-byte budget
	d.Put("a", []byte("12345"))
	d.Put("b", []byte("12345"))
	d.Put("c", []byte("12345"))

	// Three 5-byte files exceed the 10-byte budget; one must be evicted.
	present := 0
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := d.Get(k); ok {
			present++
		}
	}
	if present != 2 {
		t.Fatalf("want 2 of 3 files retained (10-byte budget), got %d", present)
	}
}
