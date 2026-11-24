package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLegacyAliasTablesAreCensused requires every production alias table to be
// named in docs/operations/legacy-alias-census.md. A new alias table without an
// owner and a retirement date fails here instead of becoming permanent by
// accident.
func TestLegacyAliasTablesAreCensused(t *testing.T) {
	expected := map[string]bool{
		"envAliases":                    true,
		"legacyTemplateAliases":         true,
		"legacyImageCompositionAliases": true,
		"entityStyleAliases":            true,
		"documentaryPhraseAliases":      true,
	}
	decl := regexp.MustCompile(`(?m)^var\s+([A-Za-z0-9_]*[Aa]liases)\s*=\s*map`)
	found := make(map[string]bool)
	root := filepath.Join(RepoRoot(), "renderinggen")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		for _, match := range decl.FindAllStringSubmatch(readText(t, path), -1) {
			found[match[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name := range found {
		if !expected[name] {
			t.Errorf("alias table %q is not registered in docs/operations/legacy-alias-census.md", name)
		}
	}
	for name := range expected {
		if !found[name] {
			t.Errorf("expected alias table %q was removed without updating the census guard", name)
		}
	}
	doc, err := os.ReadFile(filepath.Join(RepoRoot(), "docs", "operations", "legacy-alias-census.md"))
	if err != nil {
		t.Fatalf("read alias census: %v", err)
	}
	for name := range expected {
		if !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("alias census does not name %q", name)
		}
	}
}
