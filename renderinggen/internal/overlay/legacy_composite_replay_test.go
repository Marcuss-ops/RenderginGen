package overlay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func legacyCompositePlan(marker bool) []byte {
	document := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "legacy-composite-gate",
		"video_id":       "legacy-composite-gate",
		"width":          640,
		"height":         360,
		"fps_num":        24,
		"fps_den":        1,
		"duration_ms":    2000,
		"items": []any{map[string]any{
			"id":          "pair",
			"kind":        "entity_image",
			"template_id": "image_popup",
			"preset_id":   "image_focus_in",
			"start_ms":    0,
			"end_ms":      2000,
			"asset_refs": []any{
				map[string]any{"asset_id": "a", "sha256": strings.Repeat("a", 64), "url": "a.png"},
				map[string]any{"asset_id": "b", "sha256": strings.Repeat("b", 64), "url": "b.png"},
			},
			"image_layers": []any{
				map[string]any{"id": "a", "asset_id": "a", "start_ms": 0, "end_ms": 1000, "preset_id": "image_scale_in", "motion_id": "image_fade_reveal"},
				map[string]any{"id": "b", "asset_id": "b", "start_ms": 1000, "end_ms": 2000, "preset_id": "image_focus_in", "motion_id": "image_focus_reveal"},
			},
		}},
	}
	if marker {
		document["legacy_composite_replay"] = true
	}
	raw, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestNewCompositeWithoutGroupRecipeFailsClosed(t *testing.T) {
	_, err := CompileSemantic(legacyCompositePlan(false))
	if err == nil || !strings.Contains(err.Error(), "image_layers without group_motion_id; new plans must declare a certified group recipe") {
		t.Fatalf("new composite without group recipe error = %v, want fail-closed guidance", err)
	}
}

func TestSealedCompositeReplayIsMarkedAndCounted(t *testing.T) {
	result, err := CompileSemantic(legacyCompositePlan(true))
	if err != nil {
		t.Fatalf("compile sealed composite replay: %v", err)
	}
	if result.Stats.LegacyCompositeImageCount != 1 || result.Stats.LegacyCompositeReplayCount != 1 {
		t.Fatalf("legacy composite counters = %d/%d, want 1/1", result.Stats.LegacyCompositeImageCount, result.Stats.LegacyCompositeReplayCount)
	}
}

// TestLegacyCompositeReplayWindowExpires is the scheduled-removal gate for the
// sealed replay path. It is intentionally time-dependent: the documented window
// closes on legacyCompositeReplayDeadline, and leaving the path in place after
// that date must fail the unit suite instead of becoming another permanent
// compatibility branch. Removing the path removes the constant this test reads,
// so the test cannot outlive its subject silently.
func TestLegacyCompositeReplayWindowExpires(t *testing.T) {
	deadline, err := time.Parse(time.DateOnly, legacyCompositeReplayDeadline)
	if err != nil {
		t.Fatalf("legacy composite replay deadline %q is not a date: %v", legacyCompositeReplayDeadline, err)
	}
	// The window includes the whole deadline day, in UTC.
	closedAt := deadline.Add(24*time.Hour - time.Nanosecond).UTC()
	if now := time.Now().UTC(); now.After(closedAt) {
		t.Fatalf("the sealed legacy composite replay window closed at %s: delete compileLegacyCompositeImageLayers, the legacy_composite_replay marker, its counters, metrics, schema property and tests", closedAt.Format(time.RFC3339))
	}
}
