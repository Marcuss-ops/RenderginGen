package overlay

import (
	"fmt"
	"strings"
	"testing"
)

// TestStandaloneImageGridWithoutBackdrop proves params.grid lowers a
// clip_grid-equivalent screen-space grid bound to the item lifetime, with no
// blurred backdrop required.
func TestStandaloneImageGridWithoutBackdrop(t *testing.T) {
	extra := `,"params":{"grid":{"style":"lines","color":"#33CCDD","spacing_px":64,"line_width_px":1,"opacity":0.2}}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile standalone image grid: %v", err)
	}
	if len(result.Plan.Layers) != 2 {
		t.Fatalf("layers = %+v; want foreground image and grid", result.Plan.Layers)
	}
	grid := result.Plan.Layers[1]
	if grid.ID != "fx-image:image-grid" || grid.Type != "shape" || grid.Shape == nil || grid.Shape.Type != "path" {
		t.Fatalf("grid layer = %+v", grid)
	}
	if !grid.ScreenSpace {
		t.Fatalf("image grid must be screen-space: %+v", grid)
	}
	if grid.StartFrame != 0 || grid.DurationFrames == 0 {
		t.Fatalf("grid timing = [%d,%d)", grid.StartFrame, grid.DurationFrames)
	}
	if grid.Opacity == nil || *grid.Opacity != 0.2 {
		t.Fatalf("grid opacity = %v, want 0.2", grid.Opacity)
	}
}

// TestStandaloneImageGridSupportsScopeAndDots pins scope and the dot style.
func TestStandaloneImageGridSupportsScopeAndDots(t *testing.T) {
	extra := `,"params":{"grid":{"scope":"foreground","style":"dots","color":"#FFFFFF","spacing_px":48,"dot_radius_px":3,"opacity":0.2}}`
	result, err := CompileSemantic(imageEffectsPlan(extra))
	if err != nil {
		t.Fatalf("compile dot image grid: %v", err)
	}
	grid := result.Plan.Layers[len(result.Plan.Layers)-1]
	if grid.Shape == nil || grid.Shape.Type != "dot_grid" {
		t.Fatalf("dot grid geometry = %+v", grid.Shape)
	}
	disabled, err := CompileSemantic(imageEffectsPlan(`,"params":{"grid":{"enabled":false}}`))
	if err != nil {
		t.Fatalf("compile disabled image grid: %v", err)
	}
	for _, layer := range disabled.Plan.Layers {
		if strings.HasSuffix(layer.ID, ":image-grid") {
			t.Fatalf("disabled grid lowered %+v", layer)
		}
	}
}

// TestCompositeBackdropSelectsAsset proves image_backdrop works for composites
// and honours an explicit asset_id selector.
func TestCompositeBackdropSelectsAsset(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","legacy_composite_replay":true,"plan_id":"backdrop-select","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"duo","kind":"image","template_id":"image_popup","start_ms":0,"end_ms":5000,"asset_refs":[%s,%s],"image_layers":[{"id":"left","asset_id":"effect-photo","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"},{"id":"right","asset_id":"effect-photo-2","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"}],"params":{"image_backdrop":{"asset_id":"effect-photo-2","zoom":1.2,"blur_px":24}}}]}`, imageEffectsAssetRef, imageEffectsSecondAssetRef))
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite backdrop with asset_id: %v", err)
	}
	if result.Plan.Layers[0].ID != "duo:image-backdrop" {
		t.Fatalf("first layer = %+v, want duo:image-backdrop", result.Plan.Layers[0])
	}
}

// TestImageClipEffectParity proves the four clip-only pseudo-types now lower
// on image layers through the same renderer-native expansion as clips.
func TestImageClipEffectParity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
		want  []string
	}{
		{"glitch fans out", `{"type":"glitch","params":{"amount":0.5,"seed":7}}`, []string{"wave_warp", "chromatic_aberration", "noise"}},
		{"scanlines become wave warp", `{"type":"scanlines","params":{"amount":0.1,"wavelength":0.05}}`, []string{"wave_warp"}},
		{"banded grain becomes noise", `{"type":"banded_grain","params":{"intensity":1.2,"speed":2}}`, []string{"noise"}},
		{"lens distortion passes through", `{"type":"lens_distortion","params":{"curvature":0.3}}`, []string{"lens_distortion"}},
	} {
		extra := `,"params":{"effects":[` + tc.entry + `]}`
		result, err := CompileSemantic(imageEffectsPlan(extra))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var imageLayer *Layer
		for index := range result.Plan.Layers {
			if result.Plan.Layers[index].ID == "fx-image:image" {
				imageLayer = &result.Plan.Layers[index]
			}
		}
		if imageLayer == nil {
			t.Fatalf("%s: base image layer missing", tc.name)
		}
		if len(imageLayer.Effects) != len(tc.want) {
			t.Fatalf("%s: effects = %+v, want %v", tc.name, imageLayer.Effects, tc.want)
		}
		for i, want := range tc.want {
			if imageLayer.Effects[i].Type != want {
				t.Fatalf("%s: effect %d = %q, want %q", tc.name, i, imageLayer.Effects[i].Type, want)
			}
		}
	}
}
