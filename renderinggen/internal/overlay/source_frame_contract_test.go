package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// sourceFramePlan builds a clip plan with a declared card treatment. The shape
// of the JSON is what PipelineGen's clip mapper emits (source_frame nested under
// the plan, frame nested under a video overlay item).
func sourceFramePlan(t *testing.T, sourceFrame, itemFrame string, params string) []byte {
	t.Helper()
	item := ""
	if itemFrame != "" {
		item = `{
			"id": "overlay-aaaa",
			"kind": "video_overlay",
			"template_id": "VIDEO_OVERLAY",
			"motion_params": {},
			"text": "",
			"start_ms": 1000,
			"end_ms": 3000,
			"params": {` + params + `},
			"frame": ` + itemFrame + `,
			"asset_refs": [{"asset_id": "overlay-aaaa", "sha256": "` + bgTestSHA + `", "url": "assets/semantic/overlay-aaaa/overlay.mp4", "media_type": "video/mp4"}]
		}`
	}
	raw := `{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "frame-test",
		"video_id": "src",
		"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
		"duration_ms": 4000,
		"source": {"asset_id": "src", "sha256": "` + clipTestSHA + `"},
		"foreground_scale_percent": 70,
		"source_frame": ` + sourceFrame + `,
		"items": [` + item + `]
	}`
	return []byte(raw)
}

func compiledLayer(t *testing.T, raw []byte, id string) Layer {
	t.Helper()
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic: %v", err)
	}
	for _, layer := range result.Plan.Layers {
		if layer.ID == id {
			return layer
		}
	}
	ids := make([]string, 0, len(result.Plan.Layers))
	for _, candidate := range result.Plan.Layers {
		ids = append(ids, candidate.ID)
	}
	t.Fatalf("no layer with id %q in %v", id, ids)
	return Layer{}
}

// TestSourceFrameLowersToBackgroundAndShadow is the card contract: the border
// becomes the renderer-owned plate behind the clip (style.background with the
// border colour, the border width as padding and radius_px as the outer radius),
// the clip takes the concentric inner radius, and the shadow travels as
// style.shadow.
func TestSourceFrameLowersToBackgroundAndShadow(t *testing.T) {
	raw := sourceFramePlan(t,
		`{"border": {"width_px": 8, "color": "#FFFFFF", "radius_px": 24},
		  "shadow": {"color": "#000000", "opacity": 0.5, "blur_px": 24, "offset_x_px": 0, "offset_y_px": 12}}`,
		"", "")

	layer := compiledLayer(t, raw, "source")
	if layer.Style == nil || layer.Style.Background == nil {
		t.Fatalf("source layer style.background missing: %+v", layer.Style)
	}
	background := layer.Style.Background
	if background.Color != "#FFFFFF" {
		t.Errorf("background color = %q, want #FFFFFF", background.Color)
	}
	if background.Radius != 24 {
		t.Errorf("background radius = %v, want 24 (the frame's outer radius)", background.Radius)
	}
	if len(background.Padding) != 2 || background.Padding[0] != 8 || background.Padding[1] != 8 {
		t.Errorf("background padding = %v, want [8 8] (the visible frame thickness)", background.Padding)
	}
	if layer.Radius != 16 {
		t.Errorf("clip radius = %v, want 16 (radius_px - width_px)", layer.Radius)
	}
	if layer.Style.Shadow == nil {
		t.Fatal("source layer style.shadow missing")
	}
	shadow := layer.Style.Shadow
	if shadow.Color != "#000000" || shadow.Opacity != 0.5 || shadow.Blur != 24 {
		t.Errorf("shadow = %+v, want color/opacity/blur from the declaration", shadow)
	}
	if len(shadow.Offset) != 2 || shadow.Offset[0] != 0 || shadow.Offset[1] != 12 {
		t.Errorf("shadow offset = %v, want [0 12]", shadow.Offset)
	}
	// The declaration must survive the Chronon boundary: the compiled plan is
	// the only thing the engine sees.
	doc := marshalPlan(t, raw)
	for _, want := range []string{`"background"`, `"padding":[8,8]`, `"radius":16`, `"shadow"`, `"blur":24`} {
		if !strings.Contains(doc, want) {
			t.Errorf("compiled chronon plan does not carry %s:\n%s", want, doc)
		}
	}
}

// TestSourceFrameStrokeLowersToPerimeterShape: stroke is a distinct visible
// outline above the media; it must not be confused with the padded border plate.
func TestSourceFrameStrokeLowersToPerimeterShape(t *testing.T) {
	raw := sourceFramePlan(t,
		`{"stroke": {"width_px": 5, "color": "#00FF80"}, "clip_radius_px": 18}`,
		"", "")
	layers := func() []Layer {
		result, err := CompileSemantic(raw)
		if err != nil {
			t.Fatalf("CompileSemantic: %v", err)
		}
		return result.Plan.Layers
	}()
	if len(layers) < 2 || layers[0].ID != "source" || layers[1].ID != "source__stroke" {
		t.Fatalf("source/stroke order = %v, want source then source__stroke", layerIDs(layers))
	}
	stroke := layers[1]
	if stroke.Type != "shape" || stroke.Shape == nil || stroke.Shape.Type != "rounded_rect" {
		t.Fatalf("stroke layer = %+v, want rounded rectangle shape", stroke)
	}
	if stroke.Shape.Stroke == nil {
		t.Fatalf("stroke shape = %+v, want an explicit outline", stroke.Shape)
	}
	if len(stroke.Color) != 4 || stroke.Color[3] != 0 {
		t.Fatalf("stroke layer fill colour = %v, want transparent RGBA", stroke.Color)
	}
	if stroke.Shape.Stroke.Color != "#00FF80" || stroke.Shape.Stroke.Width != 5 || stroke.Shape.Radius != 18 {
		t.Errorf("stroke style/radius = %+v radius %v, want declared #00FF80/5 and radius 18", stroke.Shape.Stroke, stroke.Shape.Radius)
	}
	if len(stroke.Size) != 2 || stroke.Size[0] != 1344 || stroke.Size[1] != 756 {
		t.Errorf("stroke bounds = %v, want displayed video box [1344 756]", stroke.Size)
	}
	if stroke.Position[0] != 0 || stroke.Position[1] != 0 {
		t.Errorf("stroke position = %v, want [0 0] (the centred box centre, not the unscaled origin)", stroke.Position)
	}
	if len(stroke.Scale) != 2 || stroke.Scale[0] != 1 || stroke.Scale[1] != 1 {
		t.Errorf("stroke shape scale = %v, want unit scale because bounds are in output pixels", stroke.Scale)
	}
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic for serialized frame stroke: %v", err)
	}
	encoded, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatalf("marshal Chronon plan: %v", err)
	}
	if strings.Contains(string(encoded), `"frame_stroke"`) {
		t.Error("transient frame stroke metadata must not leak into Chronon plan")
	}
	if compiled.Plan.Schema != "chronon.render-plan.v3" || compiled.Plan.Version != 3 {
		t.Errorf("stroke plan schema = %s v%d, want chronon.render-plan.v3", compiled.Plan.Schema, compiled.Plan.Version)
	}
}

// TestSourceFrameStrokeLowersOnItemVideoOverlay pins the same behavior for
// item-level video frame declarations.
func TestSourceFrameStrokeLowersOnItemVideoOverlay(t *testing.T) {
	raw := sourceFramePlan(t, `{"shadow": {"color": "#000000"}}`,
		`{"stroke": {"width_px": 3, "color": "#FF4000"}, "clip_radius_px": 12}`,
		`"fit": "cover", "scale_percent": 75`)
	layer := compiledLayer(t, raw, overlayLayerID("overlay-aaaa")+"__stroke")
	if layer.Shape == nil || layer.Shape.Stroke == nil || layer.Shape.Stroke.Color != "#FF4000" || layer.Shape.Stroke.Width != 3 {
		t.Fatalf("item stroke layer = %+v, want the declared #FF4000 3px outline", layer)
	}
	if layer.Shape.Radius != 12 || len(layer.Size) != 2 || layer.Size[0] != 1440 || layer.Size[1] != 810 {
		t.Errorf("item stroke geometry = radius %v size %v, want displayed video box [1440 810]", layer.Shape.Radius, layer.Size)
	}
	if layer.Position[0] != 0 || layer.Position[1] != 0 {
		t.Errorf("item stroke transform position = %v, want [0 0] for the centred scaled box", layer.Position)
	}
	if len(layer.Scale) != 2 || layer.Scale[0] != 1 || layer.Scale[1] != 1 {
		t.Errorf("item stroke scale = %v, want unit scale because bounds are in output pixels", layer.Scale)
	}
}

// TestSourceFrameShadowOnlyLeavesGeometryAlone: a shadow without a border is a
// clip shadow (no plate, no inner radius), which is the native-shadow path.
func TestSourceFrameShadowOnlyLeavesGeometryAlone(t *testing.T) {
	raw := sourceFramePlan(t,
		`{"shadow": {"color": "#101010", "opacity": 0.8, "blur_px": 12, "offset_x_px": -4, "offset_y_px": 6}}`,
		"", "")
	layer := compiledLayer(t, raw, "source")
	if layer.Style == nil || layer.Style.Shadow == nil {
		t.Fatal("shadow-only frame must still declare the shadow")
	}
	if layer.Style.Background != nil {
		t.Errorf("shadow-only frame must not declare a plate, got %+v", layer.Style.Background)
	}
	if layer.Radius != 0 {
		t.Errorf("shadow-only frame must not round the clip, got radius %v", layer.Radius)
	}
}

// TestSourceFrameShadowsLowerToStyleShadows is the multi-shadow card contract:
// a `frame.shadows` stack travels as style.shadows, in declaration order, and
// never collapses to the singular form the renderer treats as a different
// declaration.
func TestSourceFrameShadowsLowerToStyleShadows(t *testing.T) {
	raw := sourceFramePlan(t,
		`{"border": {"width_px": 6, "color": "#FFFFFF", "radius_px": 20},
		  "shadows": [
		    {"color": "#000000", "opacity": 0.6, "blur_px": 10, "offset_x_px": 0, "offset_y_px": 14},
		    {"color": "#1040C0", "opacity": 0.5, "blur_px": 14, "offset_x_px": -22, "offset_y_px": 8},
		    {"color": "#C04010", "opacity": 0.5, "blur_px": 14, "offset_x_px": 22, "offset_y_px": 8}
		  ]}`,
		`{"shadow": {"color": "#000000", "opacity": 0.4, "blur_px": 8, "offset_y_px": 4}}`,
		`"fit": "cover", "scale_percent": 60`)

	layer := compiledLayer(t, raw, "source")
	if layer.Style == nil {
		t.Fatal("source layer style missing")
	}
	if layer.Style.Shadow != nil {
		t.Errorf("frame.shadows must not also lower a singular style.shadow: %+v", layer.Style.Shadow)
	}
	if len(layer.Style.Shadows) != 3 {
		t.Fatalf("style.shadows = %+v, want the three declared shadows", layer.Style.Shadows)
	}
	want := []struct {
		color   string
		blur    float64
		offsetX float64
		offsetY float64
	}{
		{"#000000", 10, 0, 14},
		{"#1040C0", 14, -22, 8},
		{"#C04010", 14, 22, 8},
	}
	for index, expected := range want {
		got := layer.Style.Shadows[index]
		if got.Color != expected.color || got.Blur != expected.blur {
			t.Errorf("shadows[%d] = %+v, want color %s blur %v", index, got, expected.color, expected.blur)
		}
		if len(got.Offset) != 2 || got.Offset[0] != expected.offsetX || got.Offset[1] != expected.offsetY {
			t.Errorf("shadows[%d] offset = %v, want [%v %v]", index, got.Offset, expected.offsetX, expected.offsetY)
		}
		if got.Opacity != 0.5 && got.Opacity != 0.6 {
			t.Errorf("shadows[%d] opacity = %v, want the declared value", index, got.Opacity)
		}
	}
	// The declaration must survive the Chronon boundary: the engine only ever
	// sees the compiled plan, and `style.shadows` is a different contract key
	// from `style.shadow`.
	doc := marshalPlan(t, raw)
	if !strings.Contains(doc, `"shadows":[`) {
		t.Fatalf("compiled chronon plan carries no style.shadows:\n%s", doc)
	}
	if strings.Count(doc, `"shadows":[`) != 1 {
		t.Errorf("the clip must carry exactly one shadow stack:\n%s", doc)
	}
	// The clip's own singular shadow and the item's singular shadow stay as
	// they were: only the frame that declared the stack changes form.
	if item := compiledLayer(t, raw, overlayLayerID("overlay-aaaa")); item.Style == nil || item.Style.Shadow == nil {
		t.Error("the item's own singular frame.shadow must keep lowering to style.shadow")
	}
}

// TestSourceFrameShadowStackIsBounded applies to every surface that decodes a
// frame: the plan-level card and the per-item (video overlay / composite image)
// card share one declaration and one validator.
func TestSourceFrameShadowStackIsBounded(t *testing.T) {
	entry := func(color string) string {
		return `{"color": "` + color + `", "opacity": 0.5, "blur_px": 8, "offset_y_px": 6}`
	}
	four := `{"shadows": [` + entry("#000000") + `,` + entry("#111111") + `,` + entry("#222222") + `,` + entry("#333333") + `]}`
	if _, err := CompileSemantic(sourceFramePlan(t, four, "", "")); err == nil {
		t.Fatal("a frame.shadows stack with four entries must be rejected")
	}
	if _, err := CompileSemantic(sourceFramePlan(t, `{"shadow": `+entry("#000000")+`, "shadows": [`+entry("#000000")+`]}`, "", "")); err == nil {
		t.Fatal("declaring both frame.shadow and frame.shadows must be rejected")
	}
	// Three entries are the documented maximum and must compile.
	three := `{"shadows": [` + entry("#000000") + `,` + entry("#111111") + `,` + entry("#222222") + `]}`
	layer := compiledLayer(t, sourceFramePlan(t, three, "", ""), "source")
	if layer.Style == nil || len(layer.Style.Shadows) != 3 {
		t.Fatalf("three shadows must compile to style.shadows, got %+v", layer.Style)
	}
}

// TestSourceFrameIsFailClosed pins every rejected declaration.
func TestSourceFrameIsFailClosed(t *testing.T) {
	cases := []struct {
		name        string
		sourceFrame string
	}{
		{"empty block", `{}`},
		{"malformed color", `{"border": {"width_px": 4, "color": "white"}}`},
		{"shorthand color", `{"border": {"width_px": 4, "color": "#FFF"}}`},
		{"negative width", `{"border": {"width_px": -1, "color": "#FFFFFF"}}`},
		{"oversized blur", `{"shadow": {"color": "#000000", "blur_px": 4096}}`},
		{"malformed stroke color", `{"stroke": {"width_px": 4, "color": "white"}}`},
		{"negative stroke width", `{"stroke": {"width_px": -1, "color": "#FFFFFF"}}`},
		{"oversized stroke width", `{"stroke": {"width_px": 65, "color": "#FFFFFF"}}`},
		{"oversized offset", `{"shadow": {"color": "#000000", "offset_x_px": 4096}}`},
		{"opacity out of range", `{"shadow": {"color": "#000000", "opacity": 2}}`},
		{"stack entry malformed color", `{"shadows": [{"color": "#000000"}, {"color": "black"}]}`},
		{"stack entry opacity out of range", `{"shadows": [{"color": "#000000", "opacity": -0.1}]}`},
		{"stack entry oversized blur", `{"shadows": [{"color": "#000000", "blur_px": 4096}]}`},
		{"stack entry oversized offset", `{"shadows": [{"color": "#000000", "offset_y_px": 4096}]}`},
		{"empty stack", `{"shadows": []}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CompileSemantic(sourceFramePlan(t, tc.sourceFrame, "", "")); err == nil {
				t.Fatalf("source_frame %s must be rejected", tc.sourceFrame)
			}
		})
	}
}

// TestSourceFrameRequiresASourceClip: a declaration with nothing to attach to
// would be dropped silently, which is exactly what the contract forbids.
func TestSourceFrameRequiresASourceClip(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "frame-no-source", "video_id": "bg",
		"width": 1280, "height": 720, "fps_num": 30, "fps_den": 1, "duration_ms": 2000,
		"source_frame": {"border": {"width_px": 8, "color": "#FFFFFF"}},
		"background": {"kind": "color", "color": [0, 0, 0, 1]},
		"items": []
	}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("a source_frame without a source clip must be rejected")
	}
}

// TestVideoOverlayItemFrameInsetsWithScale is the item-level surface: the same
// card contract applies to a video overlay item, and params.scale_percent
// insets the segment (centred, exactly like the clip's foreground scale) so the
// frame is actually visible.
func TestVideoOverlayItemFrameInsetsWithScale(t *testing.T) {
	raw := sourceFramePlan(t, `{"shadow": {"color": "#000000", "opacity": 0.4, "blur_px": 10}}`,
		`{"border": {"width_px": 6, "color": "#111111", "radius_px": 20}}`,
		`"fit": "cover", "scale_percent": 60`)

	layer := compiledLayer(t, raw, overlayLayerID("overlay-aaaa"))
	if len(layer.Scale) != 2 || layer.Scale[0] != 0.6 || layer.Scale[1] != 0.6 {
		t.Errorf("item scale = %v, want [0.6 0.6]", layer.Scale)
	}
	if len(layer.Position) != 2 || layer.Position[0] != -960 || layer.Position[1] != -540 {
		t.Errorf("item position = %v, want the centred transform [-960 -540]", layer.Position)
	}
	if layer.Style == nil || layer.Style.Background == nil {
		t.Fatal("item frame must lower to style.background")
	}
	if layer.Style.Background.Color != "#111111" || layer.Style.Background.Radius != 20 {
		t.Errorf("item plate = %+v, want the declared colour/radius", layer.Style.Background)
	}
	if layer.Radius != 14 {
		t.Errorf("item inner radius = %v, want 14", layer.Radius)
	}
	if layer.Style.Shadow != nil {
		t.Error("the item's own frame declares no shadow; the clip's shadow must not leak onto it")
	}
	// The clip's shadow-only declaration stays on the clip.
	if src := compiledLayer(t, raw, "source"); src.Style == nil || src.Style.Shadow == nil {
		t.Error("the clip shadow must stay on the source layer")
	}
}

// TestVideoOverlayScalePercentIsValidated keeps the new item control closed.
func TestVideoOverlayScalePercentIsValidated(t *testing.T) {
	for _, params := range []string{`"scale_percent": 0`, `"scale_percent": 101`, `"scale_percent": "60"`} {
		if _, err := CompileSemantic(sourceFramePlan(t, `{"shadow": {"color": "#000000"}}`, `{"border": {"width_px": 4, "color": "#FFFFFF"}}`, params)); err == nil {
			t.Errorf("params %s must be rejected", params)
		}
	}
}

func marshalPlan(t *testing.T, raw []byte) string {
	t.Helper()
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic: %v", err)
	}
	encoded, err := json.Marshal(result.Plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	return string(encoded)
}
