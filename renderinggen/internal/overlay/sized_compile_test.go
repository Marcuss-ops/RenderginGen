package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func sizedEntityPlan() []byte {
	asset := fmt.Sprintf(`{"asset_id":"portrait","sha256":%q,"url":"https://example.test/portrait.png","media_type":"image/png"}`,
		strings.Repeat("c", 64))
	return []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"sized-entity","video_id":"sized-entity",
		"width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"items":[{
			"id":"portrait","kind":"entity_image","template_id":"image_popup",
			"preset_id":"image_scale_in","image_preset_id":"image_scale_in",
			"entity_caption":"Ada Lovelace","caption_layout":"below",
			"start_ms":0,"end_ms":3000,
			"asset_refs":[%s],
			"params":{"width":480,"height":480}
		}]
	}`, asset))
}

func imageSizeOf(t *testing.T, layers []Layer, suffix string) *Layer {
	t.Helper()
	for i := range layers {
		if strings.HasSuffix(layers[i].ID, suffix) {
			return &layers[i]
		}
	}
	t.Fatalf("no layer ending in %q in %+v", suffix, layers)
	return nil
}

// TestSizedCompileMatchesThePostCompileFit is the equivalence proof for the
// inverted boundary: compiling once with the probed source sizes must produce
// exactly the geometry the historical compile-then-fit flow produced, for the
// portrait card, its caption and its premium components.
func TestSizedCompileMatchesThePostCompileFit(t *testing.T) {
	raw := sizedEntityPlan()
	sizes := map[string]ImageSize{"assets/semantic/portrait.png": {Width: 1600, Height: 900}}

	blind, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("blind compile: %v", err)
	}
	blindImage := imageSizeOf(t, blind.Plan.Layers, "portrait:image")
	if !blindImage.EntityImage {
		t.Fatal("blind compile must leave the entity-image marker for a later fit")
	}
	if _, err := FitImageLayersToSizes(blind.Plan, sizes); err != nil {
		t.Fatalf("post-compile fit: %v", err)
	}

	sized, err := CompileSemanticWithAssetSizes(raw, sizes)
	if err != nil {
		t.Fatalf("sized compile: %v", err)
	}
	sizedImage := imageSizeOf(t, sized.Plan.Layers, "portrait:image")
	if sizedImage.EntityImage {
		t.Fatal("sized compile must not leave transient geometry markers")
	}

	for _, layer := range sized.Plan.Layers {
		if layer.EntityImage || layer.DeriveWidthFromAsset {
			t.Fatalf("sized plan layer %q still awaits source geometry", layer.ID)
		}
	}

	if len(blind.Plan.Layers) != len(sized.Plan.Layers) {
		t.Fatalf("layer counts blind/sized = %d/%d", len(blind.Plan.Layers), len(sized.Plan.Layers))
	}
	for index := range sized.Plan.Layers {
		got, want := sized.Plan.Layers[index], blind.Plan.Layers[index]
		if got.ID != want.ID || got.Type != want.Type {
			t.Fatalf("layer %d identity = %q/%q, want %q/%q", index, got.ID, got.Type, want.ID, want.Type)
		}
		if fmt.Sprint(got.Size) != fmt.Sprint(want.Size) {
			t.Errorf("layer %q size = %v, want the post-fit %v", got.ID, got.Size, want.Size)
		}
		if got.BoxWidth != want.BoxWidth || got.BoxHeight != want.BoxHeight {
			t.Errorf("layer %q box = %dx%d, want %dx%d", got.ID, got.BoxWidth, got.BoxHeight, want.BoxWidth, want.BoxHeight)
		}
		if got.Fit != want.Fit {
			t.Errorf("layer %q fit = %q, want %q", got.ID, got.Fit, want.Fit)
		}
		if fmt.Sprint(got.Position) != fmt.Sprint(want.Position) {
			t.Errorf("layer %q position = %v, want %v", got.ID, got.Position, want.Position)
		}
		if got.Style != nil && want.Style != nil {
			if got.Style.FontSize != want.Style.FontSize || got.Style.MinFontSize != want.Style.MinFontSize ||
				got.Style.MaxFontSize != want.Style.MaxFontSize || got.Style.FitMode != want.Style.FitMode {
				t.Errorf("layer %q caption style = %+v, want %+v", got.ID, got.Style, want.Style)
			}
		}
	}
	if sizedImage.BoxWidth != 480 || sizedImage.BoxHeight != 270 {
		t.Fatalf("sized portrait = %dx%d, want the 16:9 480x270 card", sizedImage.BoxWidth, sizedImage.BoxHeight)
	}
}

// TestSizedCompileDerivesAnAuthoredHeight closes the standalone height-only
// contract inside lowering: the width comes from the probed aspect.
func TestSizedCompileDerivesAnAuthoredHeight(t *testing.T) {
	asset := fmt.Sprintf(`{"asset_id":"photo","sha256":%q,"url":"https://example.test/photo.png","media_type":"image/png"}`,
		strings.Repeat("d", 64))
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"sized-derive","video_id":"sized-derive",
		"width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"items":[{
			"id":"pin","kind":"image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":0,"end_ms":3000,"asset_refs":[%s],
			"params":{"height":700}
		}]
	}`, asset))
	sized, err := CompileSemanticWithAssetSizes(raw, map[string]ImageSize{
		"assets/semantic/photo.png": {Width: 1280, Height: 1519},
	})
	if err != nil {
		t.Fatalf("sized compile: %v", err)
	}
	layer := imageSizeOf(t, sized.Plan.Layers, "pin:image")
	if layer.DeriveWidthFromAsset {
		t.Fatal("sized compile still carries the derive-width marker")
	}
	if layer.BoxWidth != 590 || layer.BoxHeight != 700 {
		t.Fatalf("derived box = %dx%d, want 590x700", layer.BoxWidth, layer.BoxHeight)
	}
}

// TestSizedCompileFailsClosedWithoutAProbe proves a sized compile never falls
// back to a guessed box: the caller must probe every waiting asset.
// TestSizedCompileMatchesThePostFitOnACameraPanChain covers the strip: the
// fitted box changes each member's height while the caption stays pinned to a
// screen-space slot. Compiling with sizes must yield byte-identical layers to
// the historical fit-after-compile flow (window, position, pan offset, style
// and animation included).
func TestSizedCompileMatchesThePostFitOnACameraPanChain(t *testing.T) {
	raw := panChainPlanPayload("sized-chain-pan")
	sizes := map[string]ImageSize{
		"assets/semantic/asset-ada.jpg":   {Width: 1600, Height: 900},
		"assets/semantic/asset-grace.jpg": {Width: 1600, Height: 900},
		"assets/semantic/asset-alan.jpg":  {Width: 1600, Height: 900},
	}
	blind, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("blind compile: %v", err)
	}
	if _, err := FitImageLayersToSizes(blind.Plan, sizes); err != nil {
		t.Fatalf("post-compile fit: %v", err)
	}
	sized, err := CompileSemanticWithAssetSizes(raw, sizes)
	if err != nil {
		t.Fatalf("sized compile: %v", err)
	}
	want, err := json.Marshal(blind.Plan.Layers)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(sized.Plan.Layers)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("sized chain-pan layers differ from the post-fit result\n--- sized ---\n%s\n--- post-fit ---\n%s", got, want)
	}
}

// TestSizedCompileFitsMixedAspectEntityChain is the mixed-aspect proof for a
// single image_chain: a landscape 4:3 source uses the canonical 16:9 cover
// card, a square source keeps its square card, and a 9:16 portrait narrows the
// card instead of being squeezed into the landscape width. Every source settles
// on cover with its natural aspect, so the chain never deforms a photo.
func TestSizedCompileFitsMixedAspectEntityChain(t *testing.T) {
	asset := func(id string, letter string) map[string]any {
		return map[string]any{
			"asset_id": id, "sha256": strings.Repeat(letter, 64),
			"url": "https://example.test/" + id + ".png", "media_type": "image/png",
		}
	}
	segment := func(id, letter string, startUS, durationUS int64) map[string]any {
		return map[string]any{
			"kind": "entity_image", "template_id": "image_popup", "preset_id": "image_scale_in",
			"image_preset_id": "image_scale_in", "asset_id": id,
			"assets":   []any{asset(id, letter)},
			"start_us": startUS, "duration_us": durationUS,
			"params": map[string]any{"width": 600, "height": 600},
		}
	}
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1", "plan_id": "mixed-aspect", "video_id": "mixed-aspect",
		"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
		"items": []any{map[string]any{
			"id": "mixed-aspect", "kind": "image_chain", "template_id": "IMAGE_CHAIN",
			"start_ms": 0, "end_ms": 9000,
			"asset_refs": []any{asset("wide", "a"), asset("square", "b"), asset("tall", "c")},
			"params": map[string]any{
				"chain_camera": "fly_under_fade", "chain_count": 3,
				"chain_segments": []any{
					segment("wide", "a", 0, 3_000_000),
					segment("square", "b", 3_000_000, 3_000_000),
					segment("tall", "c", 6_000_000, 3_000_000),
				},
			},
		}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemanticWithAssetSizes(raw, map[string]ImageSize{
		"assets/semantic/wide.png":   {Width: 1600, Height: 1200},
		"assets/semantic/square.png": {Width: 1000, Height: 1000},
		"assets/semantic/tall.png":   {Width: 900, Height: 1600},
	})
	if err != nil {
		t.Fatalf("sized mixed-aspect chain: %v", err)
	}

	wide := imageSizeOf(t, result.Plan.Layers, "seg0:image")
	square := imageSizeOf(t, result.Plan.Layers, "seg1:image")
	tall := imageSizeOf(t, result.Plan.Layers, "seg2:image")
	if wide.BoxWidth != 600 || wide.BoxHeight != 337 || wide.Fit != FitCover {
		t.Errorf("wide card = %dx%d fit=%q, want the 16:9 600x337 cover card", wide.BoxWidth, wide.BoxHeight, wide.Fit)
	}
	if square.BoxWidth != 600 || square.BoxHeight != 600 || square.Fit != FitCover {
		t.Errorf("square card = %dx%d fit=%q, want 600x600 cover", square.BoxWidth, square.BoxHeight, square.Fit)
	}
	if tall.BoxWidth != 338 || tall.BoxHeight != 600 || tall.Fit != FitCover {
		t.Errorf("portrait card = %dx%d fit=%q, want the 338x600 cover card", tall.BoxWidth, tall.BoxHeight, tall.Fit)
	}
	if wide.BoxWidth == tall.BoxWidth {
		t.Errorf("portrait chain member kept the landscape card width %d instead of narrowing", tall.BoxWidth)
	}
	for _, layer := range []*Layer{wide, square, tall} {
		if layer.EntityImage {
			t.Errorf("layer %q still awaits source geometry after the sized compile", layer.ID)
		}
	}
}

// assertSizedMatchesPostFit compiles the same payload twice: blind + the
// historical fit, and once with the probed sizes. The complete layer list must
// be byte-identical, so any field the sized compile settles differently is a
// regression, not a formatting preference.
func assertSizedMatchesPostFit(t *testing.T, raw []byte, sizes map[string]ImageSize) {
	t.Helper()
	blind, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("blind compile: %v", err)
	}
	if _, err := FitImageLayersToSizes(blind.Plan, sizes); err != nil {
		t.Fatalf("post-compile fit: %v", err)
	}
	sized, err := CompileSemanticWithAssetSizes(raw, sizes)
	if err != nil {
		t.Fatalf("sized compile: %v", err)
	}
	want, err := json.Marshal(blind.Plan.Layers)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(sized.Plan.Layers)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("sized layers differ from the post-fit result\n--- sized ---\n%s\n--- post-fit ---\n%s", got, want)
	}
}

// TestSizedCompileMatchesThePostFitOnACompositeGroup covers the legacy
// composite replay with two members, captions and different source aspects:
// the group lowers every child before decorating, so the fitted box must be
// the same one the post-compile fit produced.
func TestSizedCompileMatchesThePostFitOnACompositeGroup(t *testing.T) {
	raw := compositeGroupPlanPayload("sized-composite")
	assertSizedMatchesPostFit(t, raw, map[string]ImageSize{
		"assets/semantic/wide.png": {Width: 1600, Height: 900},
		"assets/semantic/tall.png": {Width: 900, Height: 1600},
	})
}

func TestSizedCompileFailsClosedWithoutAProbe(t *testing.T) {
	if _, err := CompileSemanticWithAssetSizes(sizedEntityPlan(), map[string]ImageSize{}); err == nil {
		t.Fatal("sized compile accepted a portrait with no probed size")
	}
}
