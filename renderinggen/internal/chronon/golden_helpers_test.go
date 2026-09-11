package chronon

import (
	"encoding/json"
	"testing"
)

// jsonEqual compares two decoded JSON objects for byte-identical
// canonicalization. It ignores field order and whitespace differences while
// requiring every value to match.
func jsonEqual(t *testing.T, a, b map[string]any) bool {
	t.Helper()
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	return string(ab) == string(bb)
}
