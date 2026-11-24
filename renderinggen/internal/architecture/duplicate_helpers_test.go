package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoDuplicateFileHashPrimitive keeps hashio.File as the worker's only
// file-hashing entry point. The removed hashFileContent was byte-for-byte the
// same function; a new local copy must fail this gate instead of drifting.
func TestNoDuplicateFileHashPrimitive(t *testing.T) {
	var offenders []string
	walkProductionGo(t, filepath.Join(RepoRoot(), "renderinggen"), func(path, text string) {
		if strings.Contains(text, "func hashFile(") || strings.Contains(text, "func hashFileContent(") {
			offenders = append(offenders, path)
		}
	})
	if len(offenders) > 0 {
		t.Fatalf("worker file hashing must go through hashio.File; local primitive found in %v", offenders)
	}
	l2 := filepath.Join(RepoRoot(), "renderinggen", "internal", "storage", "l2.go")
	if !strings.Contains(readText(t, l2), "hashio.File(p)") {
		t.Fatal("storage/l2.go must verify cache bytes through hashio.File")
	}
}

// TestCLIResolversStayShared prevents the four demo commands from growing their
// own copies of the Chronon and assets-root lookup again.
func TestCLIResolversStayShared(t *testing.T) {
	var offenders []string
	walkProductionGo(t, filepath.Join(RepoRoot(), "renderinggen", "cmd"), func(path, text string) {
		if strings.Contains(text, "func resolveChronon(") || strings.Contains(text, "func resolveAssetsRoot(") {
			offenders = append(offenders, path)
		}
	})
	if len(offenders) > 0 {
		t.Fatalf("commands must use internal/cli resolvers; local copies found in %v", offenders)
	}
}

// TestFiniteUsesOneImplementation pins the numeric predicate to
// numeric.Finite. Local wrappers are allowed for call-site readability, but a
// second math.IsNaN/math.IsInf implementation is not.
func TestFiniteUsesOneImplementation(t *testing.T) {
	walkProductionGo(t, filepath.Join(RepoRoot(), "renderinggen"), func(path, text string) {
		for i, line := range strings.Split(text, "\n") {
			if !strings.Contains(line, "func finite(value float64) bool") && !strings.Contains(line, "func finiteFloat(value float64) bool") {
				continue
			}
			if !strings.Contains(line, "numeric.Finite") {
				t.Errorf("%s:%d defines a second finite predicate: %s", path, i+1, strings.TrimSpace(line))
			}
		}
	})
}

func walkProductionGo(t *testing.T, root string, visit func(path, text string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		visit(filepath.ToSlash(path), readText(t, path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
