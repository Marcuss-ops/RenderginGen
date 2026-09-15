package chronon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The engine-side contracts this package mirrors by hand (the IPC wire enums,
// the per-frame progress line) live in the sibling Chronon3d checkout. These
// tests read the ENGINE SOURCE and hold the mirror to it, which is the only way
// to notice a header or format change without an engine release note.
//
// Discovery follows the convention already used by
// internal/overlay/contract_boundary_test.go: walk up from this file to the
// go.work that joins the sibling repositories. No go.work (a standalone
// RenderingGen checkout, including this repository's own CI) means the test
// SKIPS — never fails — so the absence of the sibling is visible as a skip
// rather than a false pass.
//
// Every cross-repo test therefore has two halves: the checkout-independent half
// pins the mirrored value as a literal, and the source-reading half holds it to
// the engine.

// chrononWorkspaceRoot walks up from start looking for the go.work that joins
// the sibling repositories. It reports ok=false when there is none, so callers
// can skip instead of failing on a filesystem root they cannot see.
func chrononWorkspaceRoot(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// chrononSourceFile returns the text of a file in the sibling Chronon3d
// checkout, or skips the test when it cannot be reached.
//
// rel is the path inside the workspace (starting with "Chronon3d/") and pathEnv
// is an optional override for a checkout that is not where discovery looks (a
// packed CI cache, a read-only engine image).
func chrononSourceFile(t *testing.T, rel, pathEnv string) string {
	t.Helper()
	if path := strings.TrimSpace(os.Getenv(pathEnv)); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("%s=%s is not readable: %v", pathEnv, path, err)
		}
		return string(raw)
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate the test source; skipping the cross-repo engine contract")
	}
	root, found := chrononWorkspaceRoot(filepath.Dir(source))
	if !found {
		t.Skip("go.work / sibling repositories not checked out; skipping the cross-repo engine contract (standalone RenderingGen checkout)")
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("engine source %s not available: %v", path, err)
	}
	return string(raw)
}

// TestChrononWorkspaceRoot pins the walk both directions, so a change to
// discovery is deliberate.
func TestChrononWorkspaceRoot(t *testing.T) {
	t.Run("absent workspace reports no root", func(t *testing.T) {
		if _, found := chrononWorkspaceRoot(t.TempDir()); found {
			t.Fatal("a tree without go.work must report no workspace root")
		}
	})
	t.Run("present workspace is found from a nested directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.25.0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		got, found := chrononWorkspaceRoot(nested)
		if !found || got != root {
			t.Fatalf("chrononWorkspaceRoot(%q) = (%q, %v), want (%q, true)", nested, got, found, root)
		}
	})
}
