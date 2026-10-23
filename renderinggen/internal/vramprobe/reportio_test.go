package vramprobe

// reportio_test.go covers the one write path every document in this package
// goes through. It is worth a test of its own because the documents ARE the
// deliverable: a report that is written without its parent directory, or
// without the trailing newline the committed evidence files have, is a defect
// that only shows up when a reader (or a diff) opens the file.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteJSONFileCreatesTheDirectoryAndEndsWithANewline(t *testing.T) {
	// Two levels deep, neither of which exists: every caller passes a path it
	// was given (a flag, a derived per-run name) and cannot assume the operator
	// made the directory.
	path := filepath.Join(t.TempDir(), "nested", "deeper", "report.json")
	document := map[string]any{"schema": "test.v1", "samples": 3}
	if err := writeJSONFile(path, document); err != nil {
		t.Fatalf("writeJSONFile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the document must exist on disk: %v", err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Errorf("document must end with a newline: %q", string(raw))
	}
	if !strings.Contains(string(raw), "\n  \"schema\"") {
		t.Errorf("document must be indented: %q", string(raw))
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("document must be valid JSON: %v", err)
	}
	if decoded["schema"] != "test.v1" {
		t.Errorf("round-trip changed the document: %+v", decoded)
	}

	// A rewrite produces the same bytes, so re-running a tool does not show up
	// as a change in the evidence diff.
	before := string(raw)
	if err := writeJSONFile(path, document); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("rewriting the same document changed the bytes:\nbefore %q\nafter  %q", before, string(after))
	}
}

func TestWriteFileRefusesAnUnencodableDocument(t *testing.T) {
	// A channel cannot be marshalled: the failure must be an error naming the
	// path, not a panic and not a truncated file.
	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeJSONFile(path, make(chan int)); err == nil {
		t.Fatal("an unencodable document must be an error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a failed encode must not leave a file behind (stat err = %v)", err)
	}
}
