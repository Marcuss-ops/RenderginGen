// reportio.go owns the ONE way this package writes a document to disk.
//
// The three modes (single probe, two-runtime pool, calibration suite) each used
// to marshal, create the parent directory and write for themselves. That is
// three chances for the evidence files to disagree about indentation, the
// trailing newline, the parent directory or the permissions — and the documents
// ARE the deliverable of this tool, so a difference between them is a defect in
// the evidence rather than a cosmetic one.
package vramprobe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// writeFile writes raw to path, creating the parent directory first.
//
// The parent is created here because every caller records a path it was given
// (a flag, a derived per-run name) and none of them can assume the operator
// made the directory: a report that exists in memory but not on disk is exactly
// the "declared success without evidence" case this tool must not have.
func writeFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("vramprobe: create directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("vramprobe: write %s: %w", path, err)
	}
	return nil
}

// writeJSONFile serializes document as indented JSON with a trailing newline
// and writes it to path.
//
// The newline is part of the contract, not style: these files are diffed and
// committed as evidence, and a missing final newline shows up as a spurious
// change on every rewrite.
func writeJSONFile(path string, document any) error {
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("vramprobe: encode %s: %w", path, err)
	}
	return writeFile(path, append(raw, '\n'))
}
