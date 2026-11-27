// Command conformance runs the cross-repo boundary scan that
// internal/architecture tests as a gate.
//
// Why a command. The scan is a repository scan, not a unit under test: running it
// meant running `go test ./internal/architecture/...`, which builds and runs the
// whole architecture suite (ratchet baselines, rule self-tests, CI-shape checks)
// to answer a question an operator or a pre-commit hook asks directly — "does
// this tree still violate a boundary rule?". This makes that answer a command
// with a normal exit code, so a hook, a CI step or a human can run it without Go
// test framing, and it adds no code path the gate does not already exercise: it
// calls the same ScanAll the test calls.
//
// It deliberately does NOT own the baselines. It READS the committed ratchet
// and applies it, so a known, accepted occurrence is not re-reported as a
// failure; refreshing the ledger remains an explicit test action
// (UPDATE_CONFORMANCE_BASELINE), because a baseline that a standalone runner
// could rewrite is a gate that a runner could silence.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/architecture"
)

func main() {
	asJSON := flag.Bool("json", false, "emit violations as JSON instead of text")
	flag.Parse()

	violations, err := architecture.ScanAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance: scan: %v\n", err)
		os.Exit(2)
	}
	baseline, err := architecture.LoadBaseline(architecture.BaselinePath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance: load baseline: %v\n", err)
		os.Exit(2)
	}
	newViolations, stale := baseline.Split(violations, architecture.AbsentSibling)
	if *asJSON {
		encoded, err := json.MarshalIndent(struct {
			New   []architecture.Violation `json:"new"`
			Stale []string                 `json:"stale"`
		}{newViolations, stale}, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "conformance: encode: %v\n", err)
			os.Exit(2)
		}
		fmt.Println(string(encoded))
	} else {
		report(newViolations, stale, len(violations)-len(newViolations))
	}
	if len(newViolations) > 0 || len(stale) > 0 {
		os.Exit(1)
	}
}

// report prints one line per NEW violation plus the rule's note once per rule,
// so a reader learns what the marker means without looking it up.
func report(violations []architecture.Violation, stale []string, baselined int) {
	if len(violations) == 0 && len(stale) == 0 {
		fmt.Printf("conformance: clean (%d targets, %d baselined)\n", len(architecture.Targets()), baselined)
		return
	}
	noted := map[string]bool{}
	for _, v := range violations {
		if v.Line > 0 {
			fmt.Printf("%s:%d: [%s] %s\n", v.File, v.Line, v.Rule, v.Snippet)
		} else {
			fmt.Printf("%s: [%s]\n", v.File, v.Rule)
		}
		if !noted[v.Rule] {
			noted[v.Rule] = true
			if note := architecture.NoteFor(v.Rule); note != "" {
				fmt.Printf("    note: %s\n", note)
			}
		}
	}
	for _, entry := range stale {
		fmt.Printf("stale baseline entry (remove it): %s\n", entry)
	}
	fmt.Fprintf(os.Stderr, "conformance: %d new violation(s), %d stale baseline entr(ies), %d baselined; refresh via `make refresh-conformance-baseline`\n", len(violations), len(stale), baselined)
}
