package overlay

import (
	"fmt"
	"testing"
)

// A below-caption enters vertically. The automatic pool contains
// entity_split_side (position_x from -72px); selecting it for a below card made
// the caption slide in from the left while its final geometry stayed centred.
func TestAutomaticBelowCaptionNeverUsesHorizontalMotion(t *testing.T) {
	for i := 0; i < 256; i++ {
		identity := fmt.Sprintf("caption-%d", i)
		for _, layout := range []string{"below", ""} {
			motionID := entityCaptionMotionIDForLayout("", layout, "plan", "video", identity)
			if motionID == "" {
				t.Fatalf("layout %q identity %q: empty caption motion", layout, identity)
			}
			if motionMovesHorizontally(motionID) {
				t.Fatalf("layout %q identity %q: selected horizontal motion %q", layout, identity, motionID)
			}
		}
	}
}

// The payload remains authoritative: an explicit motion is not silently
// replaced, even when it is horizontal. Only the automatic fallback is
// constrained by the layout.
func TestExplicitCaptionMotionIsNeverFiltered(t *testing.T) {
	if got := entityCaptionMotionIDForLayout("entity_split_side", "below", "plan", "video", "item"); got != "entity_split_side" {
		t.Fatalf("explicit motion = %q, want entity_split_side", got)
	}
}

func TestBelowPoolExcludesHorizontalEntrances(t *testing.T) {
	if !containsString(entityCaptionMotionPool, "entity_split_side") {
		t.Skip("catalog no longer exposes entity_split_side")
	}
	filtered := captionMotionsWithoutHorizontalEntrance(entityCaptionMotionPool)
	if len(filtered) == 0 {
		t.Fatal("filtered below pool is empty")
	}
	if containsString(filtered, "entity_split_side") {
		t.Fatal("entity_split_side survived the below-layout filter")
	}
}
