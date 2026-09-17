package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRaw plants a file directly in the object tree, bypassing the verified
// write path. It is how every "the bytes arrived some other way" scenario is
// reproduced: a volume restore, a hand copy, bit rot, a bad migration.
func writeRaw(t *testing.T, s *Store, shard, name string, data []byte) string {
	t.Helper()
	dir := filepath.Join(s.dir, "objects", shard)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerifyAcceptsStoredObject(t *testing.T) {
	s := New(t.TempDir())
	payload := []byte("verified bytes")
	key := digestOf(payload)
	if err := s.Put(key, payload); err != nil {
		t.Fatal(err)
	}

	got, err := s.Verify(key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !got.OK || got.SHA256 != key || got.SizeBytes != int64(len(payload)) {
		t.Fatalf("verify result = %+v", got)
	}
}

// TestVerifyDetectsTamperedBytes is the reason the read-side check exists: the
// write path cannot vouch for a file that changed after it was installed.
func TestVerifyDetectsTamperedBytes(t *testing.T) {
	s := New(t.TempDir())
	original := []byte("the original bytes")
	key := digestOf(original)
	if err := s.Put(key, original); err != nil {
		t.Fatal(err)
	}

	// Same length, different content: a size check would pass, only the digest
	// catches it.
	tampered := []byte("the TAMPERED Bytes")
	if len(tampered) != len(original) {
		t.Fatalf("fixture must be length-preserving: %d vs %d", len(tampered), len(original))
	}
	if err := os.WriteFile(s.path(key), tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := s.Verify(key)
	if !errors.Is(err, ErrIntegrityViolation) {
		t.Fatalf("want ErrIntegrityViolation, got %v", err)
	}
	if got.OK {
		t.Fatal("a tampered object must not report OK")
	}
	if got.SHA256 != digestOf(tampered) {
		t.Fatalf("Verify must report the digest actually found, got %q", got.SHA256)
	}
	if !strings.Contains(got.Reason, key) {
		t.Fatalf("reason must name the address it contradicts, got %q", got.Reason)
	}
}

func TestVerifyMissingAndMalformed(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Verify(digestOf([]byte("absent"))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent object: want ErrNotFound, got %v", err)
	}
	if _, err := s.Verify("plan.json"); !errors.Is(err, ErrInvalidContentAddress) {
		t.Fatalf("malformed key: want ErrInvalidContentAddress, got %v", err)
	}
}

func TestScanReportsCleanStore(t *testing.T) {
	s := New(t.TempDir())
	for _, payload := range []string{"one", "two", "three"} {
		if err := s.Put(digestOf([]byte(payload)), []byte(payload)); err != nil {
			t.Fatal(err)
		}
	}

	report, err := s.Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !report.Clean() {
		t.Fatalf("healthy store reported dirt: %+v", report)
	}
	if report.Objects != 3 || report.Verified != 3 {
		t.Fatalf("scan = %+v, want 3 objects / 3 verified", report)
	}
}

// TestScanOnEmptyStoreIsClean pins that a fresh volume is a valid store: a scan
// that fails because the tree does not exist yet would make the check useless
// exactly when it is first wired up.
func TestScanOnEmptyStoreIsClean(t *testing.T) {
	report, err := New(t.TempDir()).Scan()
	if err != nil {
		t.Fatalf("scan of an empty store: %v", err)
	}
	if !report.Clean() || report.Objects != 0 {
		t.Fatalf("empty store scan = %+v", report)
	}
}

// TestScanFindsEveryUnaddressableShape covers the three ways an object tree can
// hold bytes that contradict their address, plus the staging leftovers a
// crashed write leaves behind. Each is reported with its own reason, because
// the operator's remedy differs: re-upload vs. fix the layout vs. delete a
// stray file.
func TestScanFindsEveryUnaddressableShape(t *testing.T) {
	s := New(t.TempDir())

	// A healthy object, to prove the scan does not simply report everything.
	healthy := []byte("healthy payload")
	healthyKey := digestOf(healthy)
	if err := s.Put(healthyKey, healthy); err != nil {
		t.Fatal(err)
	}

	// 1. Tampered content under a valid address.
	tamperedKey := digestOf([]byte("original content"))
	writeRaw(t, s, tamperedKey[:2], tamperedKey, []byte("tampered content!"))

	// 2. A filename that is not a content address at all.
	writeRaw(t, s, "ab", "plan.json", []byte("{}"))

	// 3. A valid address stored under the wrong shard.
	strayKey := digestOf([]byte("stray"))
	wrongShard := "zz"
	if strayKey[:2] == wrongShard {
		wrongShard = "yy"
	}
	writeRaw(t, s, wrongShard, strayKey, []byte("stray"))

	// 4. A leftover staging file from an interrupted upload.
	staging := writeRaw(t, s, healthyKey[:2], ".object-crashed", []byte("partial"))

	report, err := s.Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Verified != 1 {
		t.Fatalf("Verified = %d, want 1 (the healthy object)", report.Verified)
	}
	if report.Objects != 4 {
		t.Fatalf("Objects = %d, want 4 candidate objects", report.Objects)
	}
	if len(report.Failures) != 3 {
		t.Fatalf("Failures = %d, want 3: %+v", len(report.Failures), report.Failures)
	}
	if len(report.Staging) != 1 || report.Staging[0] != staging {
		t.Fatalf("Staging = %v, want [%s]", report.Staging, staging)
	}
	if report.Clean() {
		t.Fatal("a store with failures and staging leftovers must not report clean")
	}

	reasons := map[string]string{}
	for _, f := range report.Failures {
		reasons[f.Key] = f.Reason
	}
	if !strings.Contains(reasons[tamperedKey], "hash to") {
		t.Errorf("tampered object reason = %q", reasons[tamperedKey])
	}
	if !strings.Contains(reasons["plan.json"], "canonical content address") {
		t.Errorf("non-canonical name reason = %q", reasons["plan.json"])
	}
	if got := reasons[strayKey]; !strings.Contains(got, `shard "`+wrongShard+`"`) || !strings.Contains(got, strayKey[:2]) {
		t.Errorf("wrong-shard reason = %q", got)
	}
}

// TestScanRemovesNothing pins the read-only contract: an integrity check that
// repairs what it finds cannot be used as evidence.
func TestScanRemovesNothing(t *testing.T) {
	s := New(t.TempDir())
	key := digestOf([]byte("original content"))
	tampered := writeRaw(t, s, key[:2], key, []byte("tampered content!"))
	staging := writeRaw(t, s, key[:2], ".object-crashed", []byte("partial"))

	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{tampered, staging} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("scan must not touch %s: %v", p, err)
		}
	}
}
