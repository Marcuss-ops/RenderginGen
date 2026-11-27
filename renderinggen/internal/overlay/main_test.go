package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain pins the geographic catalog for this package's tests.
//
// Map compilation resolves Natural Earth through CHRONONTEMPLATE_CATALOG. The
// loader used to search the working directory's parents for a sibling
// ChrononTemplate checkout, so these tests passed on a developer machine and
// failed on a standalone CI checkout. The fixture below ships with this
// repository and carries every feature the map tests exercise; an operator can
// still override it by setting the variable before `go test`.
func TestMain(m *testing.M) {
	if strings.TrimSpace(os.Getenv("CHRONONTEMPLATE_CATALOG")) == "" {
		if wd, err := os.Getwd(); err == nil {
			catalog := filepath.Clean(filepath.Join(wd, "..", "geofeatures", "testdata", "catalog"))
			_ = os.Setenv("CHRONONTEMPLATE_CATALOG", catalog)
		}
	}
	os.Exit(m.Run())
}
