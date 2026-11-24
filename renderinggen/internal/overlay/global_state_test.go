package overlay

import (
	"sync"
	"testing"
)

// entityStyleAliasWarned is process-wide, but LoadOrStore only runs after the
// selector matched a key of the finite entityStyleAliases table. This test pins
// that bound: unknown selectors must not grow the map.
func TestEntityStyleAliasWarnedOnlyTracksCatalogAliases(t *testing.T) {
	entityStyleAliasWarned = sync.Map{}
	t.Cleanup(func() { entityStyleAliasWarned = sync.Map{} })

	if _, ok := LookupEntityStyle("definitely-not-an-entity-style-xyz"); ok {
		t.Fatal("unknown selector unexpectedly resolved")
	}
	if got := warnedAliasCount(); got != 0 {
		t.Fatalf("unknown selector grew the warning map to %d entries", got)
	}

	if _, ok := LookupEntityStyle("center_top"); !ok {
		t.Fatal("known legacy alias did not resolve")
	}
	if got := warnedAliasCount(); got != 1 {
		t.Fatalf("known alias warning map = %d entries, want 1", got)
	}
}

func warnedAliasCount() int {
	count := 0
	entityStyleAliasWarned.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
