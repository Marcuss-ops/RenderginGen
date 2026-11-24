package overlay

import (
	"strings"
	"testing"
)

func TestLegacyImageCompositionAliasesResolveToCounts(t *testing.T) {
	for alias, wantCount := range legacyImageCompositionAliases {
		composition := imageCompositionFor(alias)
		if composition == nil {
			t.Errorf("alias %q did not resolve", alias)
			continue
		}
		if composition.ImageCount != wantCount {
			t.Errorf("alias %q resolved to image count %d, want %d", alias, composition.ImageCount, wantCount)
		}
		hasCaption := composition.Caption != nil
		wantCaption := strings.HasSuffix(alias, "_with_text")
		if hasCaption != wantCaption {
			t.Errorf("alias %q caption presence = %v, want %v", alias, hasCaption, wantCaption)
		}
	}
}
