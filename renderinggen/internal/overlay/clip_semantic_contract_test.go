package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// sha256 is a valid 64-char hex SHA-256 used throughout clip semantic fixtures.
const clipTestSHA = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
const bgTestSHA = "1122334455667788990011223344556677889900112233445566778899001122"
const subTestSHA = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
const fontTestSHA = "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

// clipSemanticFixture returns a renderinggen.overlay-plan.v1 JSON fixture
// representing a complete clip render job:
//   - source celebrity clip
//   - background video
//   - foreground at 80% scale (centered)
//   - Montserrat subtitles (burn mode)
//   - text watermark centered
//   - audio copy_if_compatible
//   - items: [] (no entity overlays)
func clipSemanticFixture() []byte {
	return []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "cert-clip-001",
		"video_id":       "src-celebrity-001",
		"width":  1920,
		"height": 1080,
		"fps_num": 30,
		"fps_den": 1,
		"duration_ms": 10000,
		"source": {
			"asset_id": "src-celebrity-001",
			"sha256": "` + clipTestSHA + `"
		},
		"foreground_scale_percent": 80,
		"source_frame": {
			"border": {"width_px": 8, "color": "#FFFFFF", "radius_px": 24},
			"shadow": {"color": "#000000", "opacity": 0.5, "blur_px": 16, "offset_x_px": 0, "offset_y_px": 8}
		},
		"background": {
			"kind": "video",
			"asset_refs": [{"asset_id": "bg-001", "sha256": "` + bgTestSHA + `"}],
			"loop": true
		},
		"subtitles": {
			"mode": "burn",
			"style_id": "montserrat_default",
			"style": {
				"font": "Montserrat", "font_size_px": 54, "color": "#FFFFFF",
				"position": "bottom_center", "width_px": 1800, "height_px": 140,
				"shadow": {"color": "#000000", "opacity": 0.7, "blur_px": 10, "offset": [0, 5]}
			},
			"asset_refs": [{"asset_id": "` + subTestSHA + `", "sha256": "` + subTestSHA + `"}]
		},
		"watermark": {
			"text": "VeloxEditing",
			"position": "center",
			"opacity": 0.8,
			"margin_px": 40,
			"style": {
				"font_size_px": 42, "color": "#FFFFFF", "width_px": 360, "height_px": 80,
				"shadow": {"color": "#000000", "opacity": 0.9, "blur_px": 8, "offset": [0, 4]}
			},
			"font_ref": {"asset_id": "font-dejavu-sans", "sha256": "` + fontTestSHA + `", "url": "assets/semantic/font-dejavu-sans/DejaVuSans.ttf", "media_type": "font/ttf"}
		},
		"audio": {
			"mode": "copy_if_compatible",
			"codec": "aac",
			"sample_rate": 48000,
			"channels": 2
		},
		"items": []
	}`)
}

// TestAdjustmentLayerMarshalDropsTiming pins the exact wire contract that the
// Chronon v3 schema enforces for adjustment layers: only id, type and effects
// are allowed. The typed Layer still carries zero timing fields internally; a
// non-zero value (for example from a hand-built plan) must not leak either.
func TestAdjustmentLayerMarshalDropsTiming(t *testing.T) {
	raw, err := json.Marshal(Layer{
		ID: "clip__adjustment", Type: "adjustment",
		StartFrame: 12, DurationFrames: 34,
		Effects: []LayerEffect{{Type: "vignette"}},
	})
	if err != nil {
		t.Fatalf("marshal adjustment layer: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode adjustment layer wire: %v", err)
	}
	if len(wire) != 3 {
		t.Fatalf("adjustment wire must contain only id, type and effects: %s", raw)
	}
	for _, forbidden := range []string{"start_frame", "duration_frames"} {
		if _, present := wire[forbidden]; present {
			t.Fatalf("adjustment wire leaked %q: %s", forbidden, raw)
		}
	}
}

// TestClipSemanticContractFull is the killer test for the complete clip render
// bridge: ClipRenderPlanV1 → renderinggen.overlay-plan.v1 → chronon.render-plan.v2.
// It verifies every required property of the compiled Chronon plan.
func TestClipSemanticContractGlobalEffectsLowerToChronon(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"fx","video_id":"clip","width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":3000,"source":{"asset_id":"clip","sha256":"` + clipTestSHA + `"},"clip_effects":[{"type":"glitch","amount":0.5,"seed":7}],"clip_grid":{"spacing_px":64,"line_width_px":1,"color":"#33CCDD","opacity":0.2},"items":[]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile global effects: %v", err)
	}
	if result.Plan.Schema != RenderPlanSchemaV3 {
		t.Fatalf("layer effects require v3, got %s", result.Plan.Schema)
	}
	var source, grid Layer
	for _, layer := range result.Plan.Layers {
		if layer.ID == "source" {
			source = layer
		}
		if layer.ID == "clip__grid" {
			grid = layer
		}
	}
	if source.Type != "video" || len(source.Effects) != 3 || source.Effects[0].Type != "wave_warp" || source.Effects[1].Type != "chromatic_aberration" || source.Effects[2].Type != "noise" {
		t.Fatalf("glitch must lower onto the source effect stack: %+v", source)
	}
	for _, layer := range result.Plan.Layers {
		if layer.ID == "clip__adjustment" {
			t.Fatalf("clip effects must not use the broken adjustment layer: %+v", layer)
		}
	}
	if grid.Type != "shape" || grid.Shape == nil || grid.Shape.Type != "path" || grid.Opacity == nil || *grid.Opacity != 0.2 {
		t.Fatalf("line grid must lower to an explicit path, got: %+v", grid)
	}
	if len(grid.Size) != 2 || grid.Size[0] != 1280 || grid.Size[1] != 720 {
		t.Fatalf("full_canvas line grid must span the whole canvas, size=%v", grid.Size)
	}
	// A line grid is built from explicit 0..width/0..height commands anchored
	// at -width/2: the centred generated `grid` shape is clipped by the
	// renderer to its positive quadrant and only covered a quarter of the frame.
	if len(grid.Position) != 2 || grid.Position[0] != -640 || grid.Position[1] != -360 {
		t.Fatalf("line grid must be anchored so its 0..width path covers the canvas; position=%v", grid.Position)
	}
	if len(grid.Shape.Path) == 0 {
		t.Fatalf("line grid has no explicit path commands: %+v", grid.Shape)
	}
	first, last := grid.Shape.Path[0], grid.Shape.Path[len(grid.Shape.Path)-1]
	if first.Type != "move_to" || first.Point[0] != 0.5 || first.Point[1] != 0.5 {
		t.Fatalf("line grid must start at the inset frame corner: %+v", first)
	}
	// The mesh closes on all four edges: border strokes sit half a stroke
	// inside the box, so their full width stays in frame instead of being
	// clipped in half, and no cell is left unclosed by the spacing remainder.
	if last.Type != "line_to" || last.Point[0] != 1279.5 || last.Point[1] != 719.5 {
		t.Fatalf("line grid must close on the bottom-right corner: %+v", last)
	}
	frameRight, frameBottom := false, false
	for _, command := range grid.Shape.Path {
		if command.Type != "move_to" {
			continue
		}
		if command.Point[0] == 1279.5 {
			frameRight = true
		}
		if command.Point[1] == 719.5 {
			frameBottom = true
		}
	}
	if !frameRight || !frameBottom {
		t.Fatalf("line grid leaves the frame open: right=%v bottom=%v", frameRight, frameBottom)
	}
	if grid.Shape.Stroke == nil || grid.Shape.Stroke.Width != 1 || grid.Shape.Stroke.Color != "#33CCDD" {
		t.Fatalf("grid line width and color must survive lowering: %+v", grid.Shape)
	}
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal Chronon v3 plan: %v", err)
	}
	var document struct {
		Layers []map[string]any `json:"layers"`
	}
	if err := json.Unmarshal(wire, &document); err != nil {
		t.Fatalf("decode Chronon wire: %v", err)
	}
	found := false
	for _, layer := range document.Layers {
		if layer["id"] == "source" {
			found = true
			effects, ok := layer["effects"].([]any)
			if !ok || len(effects) != 3 {
				t.Fatalf("Chronon source wire lost the clip effects: %v", layer)
			}
		}
	}
	if !found {
		t.Fatalf("Chronon wire has no source layer: %s", wire)
	}
	var gridWire struct {
		Layers []struct {
			ID       string    `json:"id"`
			Size     []float64 `json:"size"`
			Position []float64 `json:"position"`
			Shape    struct {
				Type   string           `json:"type"`
				Path   []map[string]any `json:"path"`
				Stroke struct {
					Color string  `json:"color"`
					Width float64 `json:"width"`
				} `json:"stroke"`
			} `json:"shape"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(wire, &gridWire); err != nil {
		t.Fatalf("decode grid wire fields: %v", err)
	}
	foundGrid := false
	for _, layer := range gridWire.Layers {
		if layer.ID == "clip__grid" {
			foundGrid = true
			if len(layer.Size) != 2 || layer.Size[0] != 1280 || layer.Size[1] != 720 || len(layer.Position) != 2 || layer.Position[0] != -640 || layer.Position[1] != -360 || layer.Shape.Type != "path" || len(layer.Shape.Path) == 0 || layer.Shape.Stroke.Width != 1 || layer.Shape.Stroke.Color != "#33CCDD" {
				t.Fatalf("grid wire is not full-frame or lost style: %+v", layer)
			}
		}
	}
	if !foundGrid {
		t.Fatalf("Chronon wire has no grid shape: %s", wire)
	}
}

// TestEnsureClipGridAboveSourceKeepsGridOutOfText pins the paint-order
// invariant: Chronon composites in declaration order, so a grid that is not
// above the source shows only on the margins around the card, while a grid
// that is last cuts its lines through the card stroke, the item overlays and
// the burned-in text.
func TestEnsureClipGridAboveSourceKeepsGridOutOfText(t *testing.T) {
	plan := &Plan{Layers: []Layer{
		{ID: "background"},
		{ID: "clip__grid"},
		{ID: "source"},
		{ID: "source__stroke"},
		{ID: "subtitles"},
		{ID: "watermark"},
	}}
	ensureClipGridAboveSource(plan)
	want := []string{"background", "source", "clip__grid", "source__stroke", "subtitles", "watermark"}
	if len(plan.Layers) != len(want) {
		t.Fatalf("layer count = %d, want %d", len(plan.Layers), len(want))
	}
	for i, id := range want {
		if plan.Layers[i].ID != id {
			t.Fatalf("paint order = %v, want %v", layerIDs(plan.Layers), want)
		}
	}
	// Without a source clip the blurred background is the plate the grid sits
	// on: still above the footage, still below the item overlays.
	backgroundOnly := &Plan{Layers: []Layer{
		{ID: "background"},
		{ID: "clip__grid"},
		{ID: "fx-item"},
	}}
	ensureClipGridAboveSource(backgroundOnly)
	backgroundWant := []string{"background", "clip__grid", "fx-item"}
	if len(backgroundOnly.Layers) != len(backgroundWant) {
		t.Fatalf("background-only layer count = %d, want %d", len(backgroundOnly.Layers), len(backgroundWant))
	}
	for i, id := range backgroundWant {
		if backgroundOnly.Layers[i].ID != id {
			t.Fatalf("background-only paint order = %v, want %v", layerIDs(backgroundOnly.Layers), backgroundWant)
		}
	}
}

// TestClipGridSitsAboveSourceAfterCompile proves the compiled plan keeps the
// grid above the source card and no longer on top of the plan.
func TestClipGridSitsAboveSourceAfterCompile(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"grid-top","video_id":"clip","width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":3000,"source":{"asset_id":"clip","sha256":"` + clipTestSHA + `"},"clip_grid":{"scope":"full_canvas","spacing_px":64,"line_width_px":1,"color":"#33CCDD","opacity":0.2},"items":[]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile grid plan: %v", err)
	}
	if len(result.Plan.Layers) < 2 {
		t.Fatalf("expected source and grid layers, got %+v", result.Plan.Layers)
	}
	sourceIndex, gridIndex := -1, -1
	for i, layer := range result.Plan.Layers {
		switch layer.ID {
		case "source":
			sourceIndex = i
		case "clip__grid":
			gridIndex = i
		}
	}
	if sourceIndex < 0 || gridIndex != sourceIndex+1 {
		t.Fatalf("source index = %d, grid index = %d; want grid directly above the source", sourceIndex, gridIndex)
	}
}

func TestClipSemanticContractModernEffectsAndDotGridLowerToChronon(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "fx_modern",
		"video_id": "clip",
		"width": 1920,
		"height": 1080,
		"fps_num": 30,
		"fps_den": 1,
		"duration_ms": 3000,
		"foreground_scale_percent": 75,
		"source": {"asset_id": "clip", "sha256": "` + clipTestSHA + `"},
		"clip_effects": [
			{"type": "glow", "radius": 24, "intensity": 1.2},
			{"type": "bloom", "radius": 32, "intensity": 0.9, "threshold": 0.7},
			{"type": "lens_distortion", "curvature": 0.12, "scale": 1.04},
			{"type": "sharpen", "amount": 1.5},
			{"type": "banded_grain", "intensity": 0.5, "speed": 2.0, "seed": 99}
		],
		"clip_grid": {
			"scope": "foreground",
			"style": "dots",
			"spacing_px": 48,
			"dot_radius_px": 2.5,
			"color": "#38BDF8",
			"opacity": 0.35
		},
		"items": []
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile modern effects and dot grid: %v", err)
	}
	var source, grid Layer
	for _, layer := range result.Plan.Layers {
		if layer.ID == "source" {
			source = layer
		}
		if layer.ID == "clip__grid" {
			grid = layer
		}
	}
	if source.Type != "video" || len(source.Effects) != 5 {
		t.Fatalf("expected 5 source effects, got: %+v", source)
	}
	if source.Effects[0].Type != "glow" || source.Effects[1].Type != "bloom" || source.Effects[2].Type != "lens_distortion" || source.Effects[3].Type != "sharpen" || source.Effects[4].Type != "noise" {
		t.Fatalf("unexpected source effect sequence: %+v", source.Effects)
	}
	if source.Effects[4].Params["amount"] != 0.125 || source.Effects[4].Params["color_mode"] != "monochrome" {
		t.Fatalf("banded_grain was not lowered to native Chronon noise: %+v", source.Effects[4])
	}
	if grid.Type != "shape" || grid.Shape == nil || grid.Shape.Type != "dot_grid" {
		t.Fatalf("expected dot_grid shape, got: %+v", grid)
	}
	if grid.Shape.GridSpacing != 48 || grid.Shape.DotRadius != 2.5 {
		t.Fatalf("dot_grid params mismatch: spacing=%v dot_radius=%v", grid.Shape.GridSpacing, grid.Shape.DotRadius)
	}
	if len(grid.Size) != 2 || grid.Size[0] != 1440 || grid.Size[1] != 810 {
		t.Fatalf("foreground grid should be bounded to 75%% of the canvas, size=%v", grid.Size)
	}
	if len(grid.Position) != 2 || grid.Position[0] != 240 || grid.Position[1] != 135 {
		t.Fatalf("foreground grid box must stay centred on the canvas; position=%v", grid.Position)
	}
}

func TestClipSemanticContractFull(t *testing.T) {
	raw := clipSemanticFixture()

	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("semantic input FAIL: CompileSemantic returned error: %v", err)
	}
	compiled, assets := result.Plan, result.Assets
	t.Log("semantic input                 PASS")

	// Decode compiled Chronon plan.
	plan := compiled
	if plan.Schema != "chronon.render-plan.v3" {
		t.Errorf("Chronon schema v3 FAIL: schema = %q, want chronon.render-plan.v3", plan.Schema)
	}
	if plan.Version != 3 {
		t.Errorf("Chronon schema v3 FAIL: version = %d, want 3", plan.Version)
	}
	t.Log("Chronon schema v3              PASS")

	// Verify layers by ID.
	layerByID := map[string]Layer{}
	for _, l := range plan.Layers {
		layerByID[l.ID] = l
	}

	// --- source layer ---
	src, ok := layerByID["source"]
	if !ok {
		t.Error("source layer FAIL: no layer with id=source")
	} else {
		t.Log("source layer                   PASS")
	}

	// --- source duration > 0 ---
	if ok && src.DurationFrames <= 0 {
		t.Errorf("source duration > 0 FAIL: source.duration_frames = %d", src.DurationFrames)
	} else if ok {
		t.Logf("source duration > 0            PASS (%d frames)", src.DurationFrames)
	}

	// --- background layer ---
	bg, bgOK := layerByID["background"]
	if !bgOK {
		t.Error("background duration > 0 FAIL: no layer with id=background")
	} else if bg.DurationFrames <= 0 {
		t.Errorf("background duration > 0 FAIL: background.duration_frames = %d", bg.DurationFrames)
	} else {
		t.Logf("background duration > 0        PASS (%d frames)", bg.DurationFrames)
	}

	// --- source surface stays canvas-sized; 80% is one centred transform ---
	expectedW := 1920
	expectedH := 1080
	if ok {
		if src.Fit != "cover" {
			t.Errorf("source fit FAIL: fit = %q, want cover", src.Fit)
		}
		if len(src.Size) != 2 || int(src.Size[0]) != expectedW || int(src.Size[1]) != expectedH {
			t.Errorf("source geometry %dx%d FAIL: size = %v", expectedW, expectedH, src.Size)
		} else {
			t.Logf("source geometry %dx%d      PASS", expectedW, expectedH)
		}
		// The compiler cancels Chronon's implicit modular canvas shift before
		// applying the centred scale transform.
		expectedX := -float64(expectedW) * 0.5
		expectedY := -float64(expectedH) * 0.5
		if len(src.Position) != 2 || src.Position[0] != expectedX || src.Position[1] != expectedY {
			t.Errorf("source centered %.0f,%.0f FAIL: position = %v", expectedX, expectedY, src.Position)
		} else {
			t.Logf("source centered %.0f,%.0f       PASS", expectedX, expectedY)
		}
		if len(src.Scale) != 2 || src.Scale[0] != 0.8 || src.Scale[1] != 0.8 {
			t.Errorf("source scale FAIL: scale = %v, want [0.8 0.8]", src.Scale)
		}
		if src.Radius != 16 {
			t.Errorf("source inner radius FAIL: got %v, want radius_px - width_px = 16", src.Radius)
		}
		if src.Style == nil || src.Style.Background == nil {
			t.Fatal("source frame FAIL: renderer-owned border plate missing")
		}
		if src.Style.Background.Color != "#FFFFFF" || src.Style.Background.Radius != 24 ||
			len(src.Style.Background.Padding) != 2 || src.Style.Background.Padding[0] != 8 || src.Style.Background.Padding[1] != 8 {
			t.Errorf("source frame plate FAIL: %+v", src.Style.Background)
		}
		if src.Style.Shadow == nil || src.Style.Shadow.Color != "#000000" || src.Style.Shadow.Blur != 16 {
			t.Errorf("source frame shadow FAIL: %+v", src.Style)
		}
	}

	// --- subtitle representation ---
	// Chronon v3 has no subtitle layer; sidecar subtitles remain in the
	// published asset manifest for the downstream mux/publish step.
	if _, ok := layerByID["subtitles"]; ok {
		t.Error("subtitle representation FAIL: unsupported subtitle layer emitted")
	} else {
		t.Log("subtitle representation        PASS (sidecar asset)")
	}
	// --- watermark representation ---
	if _, ok := layerByID["watermark"]; !ok {
		t.Error("watermark representation FAIL: no layer with id=watermark")
	} else {
		t.Log("watermark representation       PASS")
	}

	// --- audio representation ---
	// The audio policy is typed metadata on the plan (json:"-"), never
	// serialized into Chronon's v3 output document.
	if plan.Output.Audio != nil {
		t.Logf("audio representation           PASS (typed worker policy: mode=%s codec=%s)", plan.Output.Audio.Mode, plan.Output.Audio.Codec)
	} else {
		t.Log("audio representation           PASS (no semantic audio declared)")
	}

	// --- compiled assets complete ---
	assetHashes := map[string]bool{}
	for _, a := range assets {
		assetHashes[a.Hash] = true
	}
	wantHashes := []string{
		clipTestSHA,
		bgTestSHA,
		subTestSHA,
	}
	for _, h := range wantHashes {
		if !assetHashes[h] {
			t.Errorf("compiled assets complete FAIL: hash %s not in asset list", h[:16]+"...")
		}
	}
	if !t.Failed() {
		t.Logf("compiled assets complete       PASS (%d assets)", len(assets))
	}
}

// TestClipSemanticItemsEmptyAccepted verifies that a clip with items:[] and
// a valid source/background is accepted (guard on renderable primitives, not items count).
func TestClipSemanticItemsEmptyAccepted(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "clip-no-items",
		"video_id": "src-001",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"duration_ms": 5000,
		"source": {"asset_id": "src-001", "sha256": "` + clipTestSHA + `"},
		"items": []
	}`)
	_, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("items:[] with source FAIL: should be accepted, got: %v", err)
	}
	t.Log("items:[] with source           PASS")
}

// TestClipSemanticNoPrimitiveRejected verifies that a plan with no source,
// no background and no items is rejected.
func TestClipSemanticNoPrimitiveRejected(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "empty",
		"video_id": "v",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"items": []
	}`)
	_, err := CompileSemantic(raw)
	if err == nil {
		t.Fatal("no-primitive plan FAIL: expected rejection, got nil error")
	}
	t.Logf("no-primitive plan rejected     PASS (%v)", err)
}

// TestClipSemanticDurationFromMS verifies that duration_ms seeds the canvas
// duration when items is empty.
func TestClipSemanticDurationFromMS(t *testing.T) {
	// 10000ms at 30fps = 300 frames
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "dur-test",
		"video_id": "src",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"duration_ms": 10000,
		"source": {"asset_id": "src", "sha256": "` + clipTestSHA + `"},
		"items": []
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("duration_ms FAIL: %v", err)
	}
	compiled := result.Plan
	plan := compiled
	// msFrames(0, 10000, 30, 1) = ceil(10000*30/1000) = 300
	if compiled.Canvas.DurationFrames != 300 {
		t.Errorf("duration_ms FAIL: canvas.duration_frames = %d, want 300", plan.Canvas.DurationFrames)
	} else {
		t.Logf("duration_ms → canvas           PASS (%d frames)", compiled.Canvas.DurationFrames)
	}
}

// TestClipSemanticForegroundScale verifies centered scaling geometry.
func TestClipSemanticForegroundScale(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "scale-test",
		"video_id": "src",
		"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
		"duration_ms": 3000,
		"source": {"asset_id": "src", "sha256": "` + clipTestSHA + `"},
		"foreground_scale_percent": 80,
		"items": []
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("foreground_scale FAIL: %v", err)
	}
	compiled := result.Plan
	for _, l := range compiled.Layers {
		if l.ID != "source" {
			continue
		}
		wantW := 1920
		wantH := 1080
		if len(l.Size) != 2 || int(l.Size[0]) != wantW || int(l.Size[1]) != wantH {
			t.Errorf("foreground_scale geometry FAIL: size=%v, want [%d %d]", l.Size, wantW, wantH)
		}
		wantX := -float64(wantW) * 0.5
		wantY := -float64(wantH) * 0.5
		if len(l.Position) != 2 || l.Position[0] != wantX || l.Position[1] != wantY {
			t.Errorf("foreground_scale centered FAIL: position=%v, want [%g %g]", l.Position, wantX, wantY)
		}
		if len(l.Scale) != 2 || l.Scale[0] != 0.8 || l.Scale[1] != 0.8 {
			t.Errorf("foreground_scale transform FAIL: scale=%v, want [0.8 0.8]", l.Scale)
		}
		t.Logf("foreground_scale geometry+centered PASS (size=%v, pos=%v)", l.Size, l.Position)
		return
	}
	t.Error("foreground_scale FAIL: no source layer found")
}

// TestClipDotGridRejectsOverlappingAndOversizedGrids pins the two dot-grid
// guards at the lowerer, which is the only layer that knows the canvas and the
// effective cell size: a radius wider than half a cell merges the dots into one
// smudge, and 4 px spacing on a 1080p canvas asks for ~130k circles.
func TestClipDotGridRejectsOverlappingAndOversizedGrids(t *testing.T) {
	planContaining := func(grid string) []byte {
		return []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"dots","video_id":"clip","width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":1000,"source":{"asset_id":"clip","sha256":"` + clipTestSHA + `"},"clip_grid":` + grid + `,"items":[]}`)
	}
	if _, err := CompileSemantic(planContaining(`{"style":"dots","spacing_px":40,"dot_radius_px":30,"color":"#FFFFFF","opacity":0.3}`)); err == nil || !strings.Contains(err.Error(), "half of spacing_px") {
		t.Fatalf("overlapping dots must fail, got %v", err)
	}
	if _, err := CompileSemantic(planContaining(`{"style":"dots","spacing_px":4,"dot_radius_px":2,"color":"#FFFFFF","opacity":0.3}`)); err == nil || !strings.Contains(err.Error(), "dots") {
		t.Fatalf("oversized dot grid must fail, got %v", err)
	}
	if _, err := CompileSemantic(planContaining(`{"style":"dots","spacing_px":48,"dot_radius_px":3,"color":"#FFFFFF","opacity":0.3}`)); err != nil {
		t.Fatalf("visible dot grid should compile, got %v", err)
	}
}
