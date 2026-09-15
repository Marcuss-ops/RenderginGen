package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baselinePath is the committed ratchet ledger.
func baselinePath() string {
	return filepath.Join(PackageDir(), "testdata", "conformance-baseline.txt")
}

// TestArchitectureConformance is the permanent cross-repo boundary gate. It
// fails when a forbidden marker reappears anywhere in the workspace, or when a
// baselined occurrence is fixed but its ledger entry is not removed.
func TestArchitectureConformance(t *testing.T) {
	vs, err := ScanAll()
	if err != nil {
		t.Fatalf("scan %v: %v", RepoRoot(), err)
	}

	base, err := LoadBaseline(baselinePath())
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}

	if os.Getenv("UPDATE_CONFORMANCE_BASELINE") == "1" {
		if err := base.Write(vs); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		t.Logf("conformance baseline rewritten with %d entries", len(vs))
		return
	}

	newViolations, stale := base.Split(vs, AbsentSibling)

	if len(newViolations) > 0 {
		var sb strings.Builder
		sb.WriteString("architecture conformance: NEW forbidden markers introduced.\n")
		sb.WriteString("PipelineGen owns semantic intent; RenderingGen owns visual lowering; Chronon owns execution.\n")
		sb.WriteString("Do not reintroduce the deleted contract — route through the canonical one.\n\n")
		for _, v := range newViolations {
			sb.WriteString("  " + v.Format() + "\n")
			if v.Snippet != "" {
				sb.WriteString("      > " + v.Snippet + "\n")
			}
		}
		t.Errorf("%s", sb.String())
	}

	if len(stale) > 0 {
		t.Errorf("architecture conformance: %d stale baseline entries — the violation is fixed, remove the ledger line (ratchet down):\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}
