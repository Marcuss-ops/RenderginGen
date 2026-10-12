package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/schemaval"
)

func TestRunWritesSchemaValidCatalogToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
	}
	if err := schemaval.ValidateFile(stdout.Bytes(), filepath.Join("..", "..", "..", "contracts", "runtime-animation-catalog.v1.schema.json")); err != nil {
		t.Fatalf("stdout catalog does not match its runtime schema: %v", err)
	}
}

func TestRunWritesCatalogToRequestedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-output", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("file mode also wrote %d bytes to stdout", stdout.Len())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("catalog file missing: %v", err)
	}
}

func TestRunReportsInvalidArgumentsAndOutputErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unexpected"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Fatalf("unexpected-argument result = code %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"-output", filepath.Join(t.TempDir(), "missing", "catalog.json")}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "write") {
		t.Fatalf("output-error result = code %d, stderr %q", code, stderr.String())
	}
}
