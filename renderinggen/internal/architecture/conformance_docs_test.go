// conformance_docs_test.go covers the CONFORMANCE.md contract: every rule the
// scan enforces is documented, and every documented rule still exists.
//
// It is its own file because it fails for a different reason from the rest: not
// "a marker reappeared" and not "the gate is misconfigured", but "the document
// and the code have drifted apart", which is a documentation fix rather than a
// code or process one.
package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConformanceDocListsEveryRule(t *testing.T) {
	docPath := filepath.Join(RepoRoot(), "CONFORMANCE.md")
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read CONFORMANCE.md: %v", err)
	}
	documented := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "| `") {
			continue
		}
		rest := trimmed[len("| `"):]
		end := strings.Index(rest, "`")
		if end <= 0 {
			continue
		}
		documented[rest[:end]] = true
	}

	known := map[string]bool{}
	for _, r := range Rules() {
		known[r.id] = true
		if !documented[r.id] {
			t.Errorf("rule %q is implemented but not listed in CONFORMANCE.md", r.id)
		}
	}
	for id := range documented {
		if !known[id] {
			t.Errorf("CONFORMANCE.md documents rule %q, which no longer exists", id)
		}
	}
}

// TestBaselineRulesAreKnown pins the ledger to the rule set: a baseline entry
// whose rule was renamed or deleted can never match a violation again, so it
// would sit in the ledger forever without this check.
