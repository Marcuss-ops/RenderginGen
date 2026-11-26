package architecture

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestProductionPanicsAreCatalogInitializers is the executable side of
// docs/evidence/code-hygiene-20261124/PANIC-AUDIT.md. A new panic must be
// classified and added here deliberately; input-driven panics are not allowed.
func TestProductionPanicsAreCatalogInitializers(t *testing.T) {
	allowed := map[string]bool{
		"renderinggen/internal/motion/catalog.go":                true,
		"renderinggen/internal/overlay/geographic_tag_styles.go": true,
		"renderinggen/internal/overlay/mapcatalog/catalog.go":    true,
	}
	root := RepoRoot()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "Upload" || name == "UploadDrive" || name == "evidence" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		text := readText(t, path)
		if !strings.Contains(text, "panic(") {
			return nil
		}
		if !allowed[rel] {
			t.Errorf("%s contains a production panic not in PANIC-AUDIT.md", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
