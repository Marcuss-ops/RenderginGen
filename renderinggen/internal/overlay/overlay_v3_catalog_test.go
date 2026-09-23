package overlay

import (
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestOverlayV3CatalogLowersEveryModernMotion exercises the same lowering used
// by semantic_compile. It keeps the catalog gate meaningful: an id may exist
// in JSON and still be unusable if it cannot produce a concrete layer plan.
func TestOverlayV3CatalogLowersEveryModernMotion(t *testing.T) {
	for _, id := range motion.Registry.AppleV3MotionIDs() {
		animation, err := animationForMotion(id, nil, "A MODERN OVERLAY", 120, 6)
		if err != nil {
			t.Fatalf("text motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 || len(animation.TextAnimators) == 0 {
			t.Fatalf("text motion %s lowered incompletely: %+v", id, animation)
		}
	}
	for _, id := range motion.Registry.ImageV3MotionIDs() {
		animation, err := animationForMotion(id, nil, "", 120, 6)
		if err != nil {
			t.Fatalf("image motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 {
			t.Fatalf("image motion %s lowered without layer tracks: %+v", id, animation)
		}
	}
}

func TestImage25DCleanV1CatalogLowersLayerOnlyWithCatalogExit(t *testing.T) {
	ids := motion.Registry.Image25DCleanV1MotionIDs()
	if len(ids) != 8 {
		t.Fatalf("clean 2.5D image catalog has %d motions, want 8", len(ids))
	}
	for _, id := range ids {
		animation, err := animationForMotion(id, nil, "", 120, 0)
		if err != nil {
			t.Fatalf("image motion %s: %v", id, err)
		}
		if animation == nil || len(animation.Tracks) == 0 || len(animation.TextAnimators) != 0 {
			t.Fatalf("image motion %s did not lower to layer tracks only: %+v", id, animation)
		}
		if !animationUses3D(animation) {
			t.Fatalf("image motion %s did not activate 3D", id)
		}
		for _, track := range animation.Tracks {
			if track.Property == "opacity" {
				last := track.Keyframes[len(track.Keyframes)-1]
				if last.Frame != 119 || last.Value != 0.0 {
					t.Fatalf("image motion %s exit = frame %d value %v, want frame 119 opacity 0", id, last.Frame, last.Value)
				}
			}
		}
	}
}

func TestRenderingGen2PresetIsARealModernTextPreset(t *testing.T) {
	definition, err := ResolveOfficialPreset(RenderingGen2PresetID)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Family != PresetText || definition.Motion.ID == "" {
		t.Fatalf("rendering gen 2 preset is incomplete: %+v", definition)
	}
	if definition.Style.Stroke == nil || definition.Style.Shadow == nil {
		t.Fatalf("rendering gen 2 preset lost legibility styling: %+v", definition.Style)
	}
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"rendering-gen-2","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"rendering_gen_2","motion_id":"depth_parallax_reveal","text":"A MODERN 2.5D TITLE","start_ms":0,"end_ms":5000}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].Animation == nil {
		t.Fatalf("rendering gen 2 did not lower to an animated layer: %+v", result.Plan.Layers)
	}
}
