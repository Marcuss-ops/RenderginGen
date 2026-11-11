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

// rejectingBackend is an L3 that refuses every write and holds nothing.
type rejectingBackend struct{ err error }

func (b rejectingBackend) Fetch(context.Context, string) ([]byte, error) { return nil, ErrNotFound }
func (b rejectingBackend) Store(context.Context, string, []byte) error   { return b.err }

// assertNoInstallTemps fails when an install left its in-flight temp file.
func assertNoInstallTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stagingNamePrefix) {
			t.Fatalf("install left temp file %q behind", entry.Name())
		}
	}
}

// TestLocalPathRejectsL3BytesThatDoNotMatchKey pins both L3 read shapes:
// streaming (InstallReader hashes while installing) and byte fallback (Get
// must hash before promoting). Bytes that do not hash to the requested key must
// never reach the workspace or the L2 cache.
func TestLocalPathRejectsL3BytesThatDoNotMatchKey(t *testing.T) {
	expected := []byte("the bytes this address promises")
	h := Hash(expected)
	cases := []struct {
		name    string
		backend Backend
	}{
		{name: "streaming L3", backend: &readerCountingBackend{inner: NewMemory()}},
		{name: "byte fallback L3", backend: NewMemory()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			// A broken/malicious L3 serving the wrong body for a valid address.
			if err := tc.backend.Store(ctx, h, []byte("a completely different body")); err != nil {
				t.Fatal(err)
			}
			c := New(tc.backend, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir(), L2MaxBytes: 1 << 20})

			path, size, err := c.LocalPath(ctx, h)
			if err == nil {
				t.Fatalf("LocalPath accepted L3 bytes that do not hash to the key: path=%q size=%d", path, size)
			}
			if path != "" || size != 0 {
				t.Fatalf("failed LocalPath returned path=%q size=%d", path, size)
			}
			if !strings.Contains(err.Error(), "does not match key") && !strings.Contains(err.Error(), "content address") {
				t.Fatalf("error does not name the content-address mismatch: %v", err)
			}
			if _, statErr := os.Stat(c.l2.path(h)); !os.IsNotExist(statErr) {
				t.Fatalf("mismatched L3 bytes were installed in L2 (stat err = %v)", statErr)
			}
			assertNoInstallTemps(t, filepath.Dir(c.l2.path(h)))
		})
	}
}

// TestClientGetRejectsL3BytesThatDoNotMatchKey is the direct byte-path
// regression: Get must not return (or promote) bytes that contradict the
// address, even when the backend is trusted to have looked them up.
func TestClientGetRejectsL3BytesThatDoNotMatchKey(t *testing.T) {
	ctx := context.Background()
	h := Hash([]byte("expected payload"))
	backend := NewMemory()
	if err := backend.Store(ctx, h, []byte("different payload")); err != nil {
		t.Fatal(err)
	}
	c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir(), L2MaxBytes: 1 << 20})

	data, err := c.Get(ctx, h)
	if !errors.Is(err, ErrContentAddressMismatch) {
		t.Fatalf("Get error = %v, want ErrContentAddressMismatch", err)
	}
	if data != nil {
		t.Fatalf("Get returned untrusted bytes: %q", data)
	}
	if _, ok := c.l1.Get(h); ok {
		t.Fatal("mismatched L3 bytes were promoted to L1")
	}
	if _, ok := c.l2.Path(h); ok {
		t.Fatal("mismatched L3 bytes were promoted to L2")
	}
	if got := c.Stats().L3Fetches; got != 0 {
		t.Fatalf("L3Fetches = %d for a rejected response, want 0", got)
	}
}

// TestClientPutRejectsLocalContentMismatch pins the write-side pre-check: a
// caller that pairs bytes with the wrong address must not poison L3 or the
// caches, whatever the backend's own verification policy is.
func TestClientPutRejectsLocalContentMismatch(t *testing.T) {
	ctx := context.Background()
	h := Hash([]byte("expected payload"))
	backend := NewMemory()
	c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir(), L2MaxBytes: 1 << 20})

	if err := c.Put(ctx, h, []byte("different payload")); !errors.Is(err, ErrContentAddressMismatch) {
		t.Fatalf("Put error = %v, want ErrContentAddressMismatch", err)
	}
	if _, err := backend.Fetch(ctx, h); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched Put reached L3: %v", err)
	}
	if _, ok := c.l1.Get(h); ok {
		t.Fatal("mismatched Put warmed L1")
	}
	if _, ok := c.l2.Path(h); ok {
		t.Fatal("mismatched Put warmed L2")
	}
}

// TestLocalPathWithoutL2RejectsHashMismatch covers the cache-disabled branch,
// which has its own temp-file path and its own hash check.
func TestLocalPathWithoutL2RejectsHashMismatch(t *testing.T) {
	ctx := context.Background()
	h := Hash([]byte("expected bytes"))
	backend := NewMemory()
	if err := backend.Store(ctx, h, []byte("wrong bytes")); err != nil {
		t.Fatal(err)
	}
	c := New(backend, Options{L1MaxBytes: 1 << 20}) // no L2 directory

	path, _, err := c.LocalPath(ctx, h)
	if err == nil || path != "" {
		t.Fatalf("LocalPath without L2 accepted wrong bytes: path=%q err=%v", path, err)
	}
	if !errors.Is(err, ErrContentAddressMismatch) {
		t.Fatalf("error = %v, want ErrContentAddressMismatch", err)
	}
}

// TestPutFileVerifiesBeforeL3 pins the file entry point: PutFile must hash the
// source before the store call, so a mispaired file never reaches L3, and it
// must keep L2 warming best-effort once L3 has accepted the bytes.
func TestPutFileVerifiesBeforeL3(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "artifact.bin")
	payload := []byte("producer bytes")
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	actual := Hash(payload)

	t.Run("mismatched address is rejected before L3", func(t *testing.T) {
		backend := NewMemory()
		wrong := Hash([]byte("some other object"))
		c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir(), L2MaxBytes: 1 << 20})
		if err := c.PutFile(ctx, wrong, source); !errors.Is(err, ErrContentAddressMismatch) {
			t.Fatalf("PutFile error = %v, want ErrContentAddressMismatch", err)
		}
		if _, err := backend.Fetch(ctx, wrong); !errors.Is(err, ErrNotFound) {
			t.Fatalf("mismatched file reached L3: %v", err)
		}
		if _, ok := c.l2.Path(wrong); ok {
			t.Fatal("mismatched file warmed L2")
		}
	})

	t.Run("matching file reaches L3 when L2 cannot be written", func(t *testing.T) {
		backend := NewMemory()
		brokenRoot := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(brokenRoot, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: brokenRoot, L2MaxBytes: 1 << 20})
		if err := c.PutFile(ctx, actual, source); err != nil {
			t.Fatalf("a cache write failure must not fail publication: %v", err)
		}
		got, err := backend.Fetch(ctx, actual)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("L3 object = %q, err=%v", got, err)
		}
		if c.Stats().L2PutErrors != 1 {
			t.Fatalf("L2PutErrors = %d, want 1", c.Stats().L2PutErrors)
		}
	})

	t.Run("matching file reaches L3 with L2 disabled", func(t *testing.T) {
		backend := NewMemory()
		c := New(backend, Options{L1MaxBytes: 1 << 20})
		if err := c.PutFile(ctx, actual, source); err != nil {
			t.Fatalf("PutFile with L2 disabled: %v", err)
		}
		got, err := backend.Fetch(ctx, actual)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("L3 object = %q, err=%v", got, err)
		}
	})
}

// TestLocalPathOversizeL3MismatchIsContentAddressMismatch pins the uncached
// fallback's error identity: an object too large for L2 is streamed to a temp
// file, and a digest contradiction there must still be the permanent sentinel,
// not an anonymous error that would be retried forever.
func TestLocalPathOversizeL3MismatchIsContentAddressMismatch(t *testing.T) {
	ctx := context.Background()
	payload := []byte("expected object")
	h := Hash(payload)
	backend := &readerCountingBackend{inner: NewMemory()}
	if err := backend.Store(ctx, h, []byte("a deliberately different object")); err != nil {
		t.Fatal(err)
	}
	c := New(backend, Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir(), L2MaxBytes: 1})

	path, size, err := c.LocalPath(ctx, h)
	if !errors.Is(err, ErrContentAddressMismatch) {
		t.Fatalf("LocalPath error = %v, want ErrContentAddressMismatch", err)
	}
	if path != "" || size != 0 {
		t.Fatalf("failed LocalPath returned path=%q size=%d", path, size)
	}
}

// TestPutDoesNotWarmCachesWhenL3Rejects pins write ordering: L3 is the source
// of truth, so a rejected store must leave no L1/L2 entry that a later read
// could serve as if the object existed.
func TestPutDoesNotWarmCachesWhenL3Rejects(t *testing.T) {
	ctx := context.Background()
	payload := []byte("cache-warming-payload")
	h := Hash(payload)
	c := New(rejectingBackend{err: errors.New("L3 rejected the write")},
		Options{L1MaxBytes: 1 << 20, L2Dir: t.TempDir(), L2MaxBytes: 1 << 20})

	if err := c.Put(ctx, h, payload); err == nil {
		t.Fatal("Put succeeded despite L3 rejecting the write")
	}
	if _, ok := c.l1.Get(h); ok {
		t.Fatal("L1 was warmed by a rejected L3 write")
	}
	if _, ok := c.l2.Path(h); ok {
		t.Fatal("L2 was warmed by a rejected L3 write")
	}
	if data, err := c.Get(ctx, h); err == nil {
		t.Fatalf("Get served bytes after a rejected L3 write: %q", data)
	}
}

// TestClientGetHealsCorruptL2FromL3 pins the end-to-end recovery behind the L2
// verification: a corrupt cache file is a miss, the correct object is fetched
// from L3, the cache is repaired, and the caller sees the right bytes.
func TestClientGetHealsCorruptL2FromL3(t *testing.T) {
	ctx := context.Background()
	payload := []byte("cache-heal-payload")
	h := Hash(payload)
	dir := t.TempDir()
	opts := Options{L1MaxBytes: 1 << 20, L2Dir: dir, L2MaxBytes: 1 << 20}
	backend := &countingBackend{inner: NewMemory()}

	writer := New(backend, opts)
	if err := writer.Put(ctx, h, payload); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "assets", h[:2], h)
	tampered := append([]byte(nil), payload...)
	tampered[0] ^= 0xff
	if err := os.WriteFile(p, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}

	// A fresh client starts with an empty L1, so it must consult the corrupt L2.
	reader := New(backend, opts)
	data, err := reader.Get(ctx, h)
	if err != nil {
		t.Fatalf("Get did not heal a corrupt L2 entry: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("Get returned %q, want the L3 payload", data)
	}
	if backend.calls != 1 {
		t.Fatalf("L3 fetches = %d, want 1", backend.calls)
	}
	if got := reader.Stats().L2Corrupt; got != 1 {
		t.Fatalf("L2Corrupt = %d, want 1", got)
	}
	healed, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(healed, payload) {
		t.Fatalf("L2 was not repaired: %q, err=%v", healed, err)
	}

	// The repaired object is promoted to L1, so a second read is local.
	if _, err := reader.Get(ctx, h); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 {
		t.Fatalf("second Get fetched L3 again: calls=%d", backend.calls)
	}
}

// TestLocalPathHealsCorruptL2FromL3 is the streaming counterpart: a corrupt L2
// file must not fail an asset resolution; it is dropped, re-fetched and
// reinstalled with verified bytes.
func TestLocalPathHealsCorruptL2FromL3(t *testing.T) {
	ctx := context.Background()
	payload := []byte("streaming-heal-payload")
	h := Hash(payload)
	dir := t.TempDir()
	opts := Options{L1MaxBytes: 1 << 20, L2Dir: dir, L2MaxBytes: 1 << 20}
	backend := &readerCountingBackend{inner: NewMemory()}

	writer := New(backend, opts)
	if err := writer.Put(ctx, h, payload); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "assets", h[:2], h)
	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-1] ^= 0xff
	if err := os.WriteFile(p, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}

	reader := New(backend, opts)
	path, size, err := reader.LocalPath(ctx, h)
	if err != nil {
		t.Fatalf("LocalPath did not heal a corrupt L2 entry: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("resolved bytes = %q, err=%v", data, err)
	}
	if backend.calls.Load() != 1 {
		t.Fatalf("L3 fetches = %d, want 1", backend.calls.Load())
	}
	if got := reader.Stats().L2Corrupt; got != 1 {
		t.Fatalf("L2Corrupt = %d, want 1", got)
	}
	if _, ok := reader.l2.Path(h); !ok {
		t.Fatal("healed L2 entry does not verify")
	}
}

// TestL2InstallReaderRejectsHashAndSizeMismatches pins the streaming install
// checks in isolation: neither a wrong digest nor a wrong declared length may
// leave an object or a temp file in the cache.
func TestL2InstallReaderRejectsHashAndSizeMismatches(t *testing.T) {
	payload := []byte("streamed object bytes")
	key := Hash(payload)
	cases := []struct {
		name         string
		key          string
		data         []byte
		size         int64
		wantMismatch bool
	}{
		{name: "hash mismatch", key: key, data: []byte("different content"), size: int64(len("different content")), wantMismatch: true},
		{name: "size mismatch", key: key, data: payload, size: int64(len(payload)) + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDiskCache(t.TempDir(), 0)
			_, _, err := d.InstallReader(tc.key, bytes.NewReader(tc.data), tc.size)
			if err == nil {
				t.Fatal("InstallReader accepted a stream that contradicts its key/size")
			}
			if tc.wantMismatch && !errors.Is(err, ErrContentAddressMismatch) {
				t.Fatalf("InstallReader error = %v, want ErrContentAddressMismatch", err)
			}
			if _, err := os.Stat(d.path(tc.key)); !os.IsNotExist(err) {
				t.Fatalf("failed install left an object (stat err = %v)", err)
			}
			assertNoInstallTemps(t, filepath.Dir(d.path(tc.key)))
		})
	}
}
