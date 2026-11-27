package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func putFixture(t *testing.T, st *Store, body string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	key := hex.EncodeToString(sum[:])
	if err := st.Put(key, []byte(body)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	return key
}

func TestCollectGarbageKeepsReferencedAndRemovesOrphans(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	referenced := putFixture(t, st, "referenced bytes")
	orphan := putFixture(t, st, "orphan bytes")

	// Dry run reports the orphan and deletes nothing.
	report, err := st.CollectGarbage(GCOptions{Referenced: map[string]struct{}{referenced: {}}})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if report.Scanned != 2 || report.Referenced != 1 || len(report.Orphans) != 1 {
		t.Fatalf("dry-run report = %+v, want 2 scanned / 1 referenced / 1 orphan", report)
	}
	if report.Deleted != 0 {
		t.Fatalf("dry run deleted %d objects", report.Deleted)
	}
	if report.Orphans[0].Key != orphan {
		t.Fatalf("dry-run orphan = %s, want %s", report.Orphans[0].Key, orphan)
	}
	if _, _, err := st.Open(orphan); err != nil {
		t.Fatalf("dry run removed the orphan: %v", err)
	}

	report, err = st.CollectGarbage(GCOptions{
		Referenced: map[string]struct{}{referenced: {}},
		Delete:     true,
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if report.Deleted != 1 || report.DeletedSize != int64(len("orphan bytes")) {
		t.Fatalf("delete report = %+v, want 1 object / %d bytes", report, len("orphan bytes"))
	}
	if _, _, err := st.Open(orphan); err == nil {
		t.Fatal("the orphan was not removed")
	}
	if _, _, err := st.Open(referenced); err != nil {
		t.Fatalf("a referenced object was removed: %v", err)
	}
}

func TestCollectGarbageHonoursMinAge(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	referenced := putFixture(t, st, "referenced")
	young := putFixture(t, st, "young orphan")

	report, err := st.CollectGarbage(GCOptions{
		Referenced: map[string]struct{}{referenced: {}},
		Delete:     true,
		MinAge:     24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.KeptYoung != 1 || report.Deleted != 0 {
		t.Fatalf("min-age report = %+v, want the young orphan kept", report)
	}
	if _, _, err := st.Open(young); err != nil {
		t.Fatalf("min-age deleted a young orphan: %v", err)
	}

	// The same object becomes eligible once the clock moves past the window.
	report, err = st.CollectGarbage(GCOptions{
		Referenced: map[string]struct{}{referenced: {}},
		Delete:     true,
		MinAge:     24 * time.Hour,
		Now:        time.Now().Add(48 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Deleted != 1 {
		t.Fatalf("expired orphan was not deleted: %+v", report)
	}
}

func TestCollectGarbageCollectsMalformedAndStagingFiles(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	referenced := putFixture(t, st, "referenced")

	objects := filepath.Join(dir, "objects")
	if err := os.MkdirAll(filepath.Join(objects, "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	malformed := filepath.Join(objects, "zz", "not-an-address")
	if err := os.WriteFile(malformed, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(objects, "aa", stagingPrefix+"123")
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staging, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := st.CollectGarbage(GCOptions{
		Referenced: map[string]struct{}{referenced: {}},
		Delete:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orphans) != 1 || !strings.Contains(report.Orphans[0].Reason, "canonical") {
		t.Fatalf("malformed file was not reported as an orphan: %+v", report.Orphans)
	}
	if len(report.Staging) != 1 {
		t.Fatalf("staging leftover was not reported: %+v", report.Staging)
	}
	if report.Deleted != 2 {
		t.Fatalf("deleted = %d, want malformed + staging", report.Deleted)
	}
	for _, path := range []string{malformed, staging} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after delete", path)
		}
	}
	if _, _, err := st.Open(referenced); err != nil {
		t.Fatalf("referenced object removed: %v", err)
	}
}

func TestCollectGarbageRefusesEmptyReferenceSet(t *testing.T) {
	st := New(t.TempDir())
	if _, err := st.CollectGarbage(GCOptions{}); err == nil {
		t.Fatal("collection with no references was accepted")
	}
}

func TestLoadReferencedKeys(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("a", 64)
	path := filepath.Join(dir, "keys.txt")
	body := "# exported from render_artifacts\n\n" + key + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := LoadReferencedKeys(path)
	if err != nil {
		t.Fatalf("load keys: %v", err)
	}
	if _, ok := keys[key]; !ok || len(keys) != 1 {
		t.Fatalf("keys = %v, want only %s", keys, key)
	}

	bad := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(bad, []byte("not-a-content-address\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReferencedKeys(bad); err == nil {
		t.Fatal("a non-canonical reference was accepted")
	}
}
