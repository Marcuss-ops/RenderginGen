package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
