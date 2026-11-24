package architecture

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestOverlayRootFilesStayUnder800 keeps the post-split package navigable. Test
// files are excluded: the golden suites are intentionally large data tables.
func TestOverlayRootFilesStayUnder800(t *testing.T) {
	root := filepath.Join(RepoRoot(), "renderinggen", "internal", "overlay")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		lines := strings.Count(readText(t, path), "\n")
		if lines > 800 {
			t.Errorf("%s has %d lines, above the 800-line overlay limit", path, lines)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPrescaleSubpackageIsIndependent pins the processor extraction boundary:
// the subpackage may use storage, but it must never import the processor root
// or the render-plan packages it is meant to be independent from.
func TestPrescaleSubpackageIsIndependent(t *testing.T) {
	root := filepath.Join(RepoRoot(), "renderinggen", "internal", "processor", "prescale")
	imports := []string{
		"internal/processor\"",
		"internal/overlay\"",
		"internal/queue\"",
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		text := readText(t, path)
		for _, forbidden := range imports {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s imports %s; prescale must stay a pure leaf", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
