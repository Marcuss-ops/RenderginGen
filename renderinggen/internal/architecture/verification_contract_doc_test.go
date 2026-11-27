package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConformanceDocumentsTheVerificationContract keeps the published policy
// table from drifting away from the enforced one. It is deliberately a content
// check, not a prose snapshot: every policy must appear in the verification
// section, and the two properties a reader relies on (strict is worker-only
// toward Chronon, and absent evidence fails certify/strict) must be stated.
func TestConformanceDocumentsTheVerificationContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "CONFORMANCE.md"))
	if err != nil {
		t.Fatalf("read CONFORMANCE.md: %v", err)
	}
	text := string(body)
	const heading = "## Output verification contract"
	index := strings.Index(text, heading)
	if index < 0 {
		t.Fatalf("CONFORMANCE.md has no %q section", heading)
	}
	section := text[index:]
	if next := strings.Index(section[len(heading):], "\n## "); next >= 0 {
		section = section[:len(heading)+next]
	}

	for _, policy := range []string{"| fast |", "| sample |", "| normal |", "| certify |", "| strict |"} {
		if !strings.Contains(section, policy) {
			t.Errorf("verification contract does not document policy %s", policy)
		}
	}
	for _, required := range []string{
		"worker-only spelling",
		"fails the job",
		"not_required",
		"composition_prediction_unverifiable",
	} {
		if !strings.Contains(section, required) {
			t.Errorf("verification contract lost the required statement %q", required)
		}
	}
}
