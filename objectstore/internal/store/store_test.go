package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// digestOf is the test-side authority for the content-address contract: it
// derives the key the same way every producer does (lowercase SHA-256 hex).
func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// objectDir is the directory an object at key lives in.
func objectDir(s *Store, key string) string {
	return filepath.Dir(s.path(key))
}

// assertNoTempFiles fails when a write left its staging file behind. A
// half-written upload that keeps its bytes on disk under a hidden name is
// still disk exhaustion, and a later retry would race it.
func assertNoTempFiles(t *testing.T, s *Store, key string) {
	t.Helper()
	entries, err := os.ReadDir(objectDir(s, key))
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read object dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".object-") {
			t.Fatalf("write left staging file %q behind", e.Name())
		}
	}
}

func TestPutGet(t *testing.T) {
	s := New(t.TempDir())
	payload := []byte("hello")
	key := digestOf(payload)
	if err := s.Put(key, payload); err != nil {
		t.Fatal(err)
	}
	data, err := s.Get(key)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("get: %v %q", err, data)
	}
}

func TestPutReaderStreamsAndInstalls(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	payload := bytes.Repeat([]byte("x"), 64*1024)
	key := digestOf(payload)
	if err := s.PutReader(key, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(key)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("streamed get: %v, %d bytes", err, len(got))
	}
	if _, err := os.Stat(s.path(key)); err != nil {
		t.Fatalf("object path missing: %v", err)
	}
}

// TestPutVerifiesEmptyObject keeps the strictness from being "reject anything
// unusual": the empty object is a legitimate content-addressed object and its
// address is the well-known SHA-256 of zero bytes.
func TestPutVerifiesEmptyObject(t *testing.T) {
	s := New(t.TempDir())
	key := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if err := s.Put(key, nil); err != nil {
		t.Fatalf("empty object: %v", err)
	}
	data, err := s.Get(key)
	if err != nil || len(data) != 0 {
		t.Fatalf("empty object get: %v %d bytes", err, len(data))
	}
}

func TestGetMissing(t *testing.T) {
	s := New(t.TempDir())
	key := digestOf([]byte("never stored"))
	if _, err := s.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestPutRejectsContentAddressMismatch is the P0 regression: before this, the
// bytes of one image could be installed under the address of another and every
// later HEAD/GET reported the address as present and trustworthy.
func TestPutRejectsContentAddressMismatch(t *testing.T) {
	s := New(t.TempDir())
	want := []byte("the bytes that belong at this address")
	key := digestOf(want)
	other := []byte("an entirely different payload")

	err := s.Put(key, other)
	if !errors.Is(err, ErrContentAddressMismatch) {
		t.Fatalf("want ErrContentAddressMismatch, got %v", err)
	}
	// The address must be untouched: a later reader has to get 404, never the
	// wrong bytes.
	if _, err := s.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected content left an object at %s: %v", key, err)
	}
	if _, _, err := s.Open(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open of a rejected address: %v", err)
	}
	assertNoTempFiles(t, s, key)

	// The same wrong bytes under their OWN (correct) address must be accepted,
	// so the rejection is about the address, not about the payload.
	if err := s.Put(digestOf(other), other); err != nil {
		t.Fatalf("correct address rejected: %v", err)
	}
}

// TestPutRejectsMalformedContentAddress pins the key grammar: one digest, one
// spelling. Uppercase hex is rejected instead of normalised, because accepting
// both would put one content at two on-disk addresses.
func TestPutRejectsMalformedContentAddress(t *testing.T) {
	s := New(t.TempDir())
	valid := digestOf([]byte("hello"))
	cases := map[string]string{
		"empty":            "",
		"short name":       "abc",
		"uppercase hex":    strings.ToUpper(valid),
		"mixed case hex":   strings.ToUpper(valid[:32]) + valid[32:],
		"too short":        valid[:SHA256HexLen-1],
		"too long":         valid + "a",
		"sha256 prefixed":  "sha256:" + valid,
		"non-hex":          strings.Repeat("z", SHA256HexLen),
		"logical path":     "objects/plan.json",
		"path traversal":   "../../etc/passwd",
		"trailing newline": valid + "\n",
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.Put(key, []byte("hello")); !errors.Is(err, ErrInvalidContentAddress) {
				t.Fatalf("Put(%q): want ErrInvalidContentAddress, got %v", key, err)
			}
			if err := s.PutReader(key, bytes.NewReader([]byte("hello")), -1); !errors.Is(err, ErrInvalidContentAddress) {
				t.Fatalf("PutReader(%q): want ErrInvalidContentAddress, got %v", key, err)
			}
			if _, err := s.Get(key); !errors.Is(err, ErrInvalidContentAddress) {
				t.Fatalf("Get(%q): want ErrInvalidContentAddress, got %v", key, err)
			}
			if _, _, err := s.Open(key); !errors.Is(err, ErrInvalidContentAddress) {
				t.Fatalf("Open(%q): want ErrInvalidContentAddress, got %v", key, err)
			}
		})
	}
}

func TestCanonicalContentAddress(t *testing.T) {
	valid := digestOf([]byte("hello"))
	if !CanonicalContentAddress(valid) {
		t.Fatalf("rejected a canonical address: %s", valid)
	}
	for _, invalid := range []string{"", "abc", strings.ToUpper(valid), valid + "0"} {
		if CanonicalContentAddress(invalid) {
			t.Fatalf("accepted non-canonical address: %q", invalid)
		}
	}
}

// TestPutRejectsDeclaredLengthMismatch: Content-Length is a claim like any
// other, and a write whose byte count disagrees with it must not install.
func TestPutRejectsDeclaredLengthMismatch(t *testing.T) {
	s := New(t.TempDir())
	payload := []byte("hello")
	key := digestOf(payload)
	if err := s.PutReader(key, bytes.NewReader(payload), int64(len(payload))+1); err == nil {
		t.Fatal("want an error for a declared length that disagrees with the body")
	} else if errors.Is(err, ErrInvalidContentAddress) || errors.Is(err, ErrContentAddressMismatch) {
		t.Fatalf("length mismatch must be its own failure, got %v", err)
	}
	if _, err := s.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("length mismatch installed an object: %v", err)
	}
	assertNoTempFiles(t, s, key)
}

// TestStoreExposesNoUnverifiedWritePath is the anti-regression gate for the
// content-address invariant. The rule "every object-store write verifies its
// digest" is only meaningful if there is no second, unverified write method to
// reach for, so the exported surface of *Store is pinned EXACTLY. A future
// PutUnverified/PutTrusting/PutRaw therefore fails this test instead of quietly
// re-opening the hole that let one image's bytes be installed under another
// image's address.
//
// The list is exhaustive on purpose: adding a method means editing it here,
// which forces the author to classify it. Only Put and PutReader install bytes
// (both verify); Verify, Scan and Capabilities are read-only, and that property
// is itself pinned — TestScanRemovesNothing proves a scan touches nothing on
// disk, and Verify only ever opens a file for reading. The gate proved its
// worth while this file was being written: adding Capabilities() as a method
// failed the build until it was classified here, which is the intended
// build-time conversation rather than a silent regression.
func TestStoreExposesNoUnverifiedWritePath(t *testing.T) {
	typ := reflect.TypeOf(&Store{})
	var methods []string
	for i := 0; i < typ.NumMethod(); i++ {
		methods = append(methods, typ.Method(i).Name)
	}
	sort.Strings(methods)

	want := []string{"Capabilities", "Get", "Open", "Put", "PutReader", "Scan", "Verify"}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("exported *store.Store methods = %v, want %v; a NEW method must be classified as read-only, or verify the content address if it installs bytes", methods, want)
	}

	// The installers are exactly the two verified write methods. Without this,
	// the exhaustive list above would still pass if someone swapped a read-only
	// helper for an installer of the same arity.
	installers := map[string]bool{"Put": true, "PutReader": true}
	for _, name := range methods {
		if installers[name] {
			continue
		}
		if strings.Contains(strings.ToLower(name), "put") || strings.Contains(strings.ToLower(name), "write") || strings.Contains(strings.ToLower(name), "store") {
			t.Fatalf("method %q looks like an installer but is not on the verified-write list", name)
		}
	}
}

// TestPutReinstallIsIdempotent: re-publishing identical bytes under the same
// address is a no-op success (retry-safe), not an error.
func TestPutReinstallIsIdempotent(t *testing.T) {
	s := New(t.TempDir())
	payload := []byte("idempotent")
	key := digestOf(payload)
	for i := 0; i < 3; i++ {
		if err := s.Put(key, payload); err != nil {
			t.Fatalf("republish %d: %v", i, err)
		}
	}
	data, err := s.Get(key)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("get after republish: %v %q", err, data)
	}
	assertNoTempFiles(t, s, key)
}
