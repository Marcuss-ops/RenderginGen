package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func compileRuntimeStylePlan(t *testing.T, params, style string) (Layer, error) {
	t.Helper()
	raw := `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"runtime-style","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"Runtime style","start_ms":0,"end_ms":3000,"params":` + params + `,"style":` + style + `}]}`
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		return Layer{}, err
	}
	return result.Plan.Layers[0], nil
}

func TestRuntimeTextStyleOverrides(t *testing.T) {
	layer, err := compileRuntimeStylePlan(t,
		`{"font_family":"inter","glow_size":28.5,"stroke_size":5}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Style == nil || layer.Style.Font != "assets/fonts/Inter.ttf" {
		t.Fatalf("runtime font override = %+v", layer.Style)
	}
	if layer.Style.Glow == nil || layer.Style.Glow.Radius != 28.5 {
		t.Fatalf("runtime glow override = %+v", layer.Style.Glow)
	}
	if layer.Style.Stroke == nil || layer.Style.Stroke.Width != 5 {
		t.Fatalf("runtime stroke override = %+v", layer.Style.Stroke)
	}
}

func TestRuntimeTextStyleNumericShadowAndFontSize(t *testing.T) {
	layer, err := compileRuntimeStylePlan(t,
		`{"font_size_px":84,"shadow_blur_px":12,"shadow_opacity":0.6,"shadow_offset_x_px":-3,"shadow_offset_y_px":7}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Style.FontSize != 84 {
		t.Fatalf("font size = %v, want 84", layer.Style.FontSize)
	}
	if layer.Style.Shadow == nil || layer.Style.Shadow.Blur != 12 || layer.Style.Shadow.Opacity != 0.6 || len(layer.Style.Shadow.Offset) != 2 || layer.Style.Shadow.Offset[0] != -3 || layer.Style.Shadow.Offset[1] != 7 {
		t.Fatalf("runtime shadow override = %+v", layer.Style.Shadow)
	}
}

func TestRuntimeStyleObjectOverridesParams(t *testing.T) {
	layer, err := compileRuntimeStylePlan(t,
		`{"font_family":"poppins","glow_size":18,"stroke_size":4}`, `{"font_family":"dejavu_sans","glow_size":0,"stroke_size":0}`)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Style.Font != officialCyrillicFontPath {
		t.Fatalf("style font did not take precedence: %q", layer.Style.Font)
	}
	if len(layer.Style.Font) == 0 {
		t.Fatal("expected the runtime font path to be materialized")
	}
	if layer.Style.Glow != nil || layer.Style.Stroke != nil {
		t.Fatalf("zero overrides must disable glow and stroke: %+v", layer.Style)
	}
}

func TestRuntimeStyleOverrideValidation(t *testing.T) {
	for _, tc := range []struct {
		name, params string
		want         string
	}{
		{"unknown-font", `{"font_family":"/tmp/unsafe.ttf"}`, "unsupported"},
		{"negative-glow", `{"glow_size":-1}`, "glow_size"},
		{"oversized-glow", `{"glow_size":257}`, "glow_size"},
		{"oversized-stroke", `{"stroke_size":65}`, "stroke_size"},
		{"wrong-type", `{"stroke_size":"thick"}`, "stroke_size"},
		{"zero-font-size", `{"font_size_px":0}`, "font_size_px"},
		{"negative-font-size", `{"font_size_px":-12}`, "font_size_px"},
		{"oversized-font-size", `{"font_size_px":513}`, "font_size_px"},
		{"negative-shadow-blur", `{"shadow_blur_px":-1}`, "shadow_blur_px"},
		{"oversized-shadow-blur", `{"shadow_blur_px":257}`, "shadow_blur_px"},
		{"shadow-opacity-above-one", `{"shadow_opacity":1.5}`, "shadow_opacity"},
		{"negative-shadow-opacity", `{"shadow_opacity":-0.1}`, "shadow_opacity"},
		{"shadow-offset-out-of-range", `{"shadow_offset_x_px":257}`, "shadow_offset_x_px"},
		{"shadow-offset-wrong-type", `{"shadow_offset_y_px":"down"}`, "shadow_offset_y_px"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileRuntimeStylePlan(t, tc.params, `{}`)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestRuntimeStyleOverridesIgnoreMotionParamsAndKeepStylePrecedence(t *testing.T) {
	const plan = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"runtime-style-motion-params","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"typewriter_clean","text":"Motion params stay separate","motion_params":{"enter_frames":24,"glow_size":999},"params":{"glow_size":10,"position_x":50},"style":{"glow_size":20},"start_ms":0,"end_ms":3000}]}`
	result, err := CompileSemantic([]byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	layer := result.Plan.Layers[0]
	if layer.Style.Glow == nil || layer.Style.Glow.Radius != 20 {
		t.Fatalf("style override did not win without validating motion params as style: %+v", layer.Style.Glow)
	}
	if len(layer.Position) != 2 || layer.Position[0] != 50 {
		t.Fatalf("unrelated params were not preserved: position=%v", layer.Position)
	}
	for _, animator := range layer.TextAnimators {
		if animator.ID == "" {
			t.Fatal("motion_params were not delivered to the typewriter motion")
		}
	}
}

func TestRuntimeFontFamilyIsCaseSensitiveClosedEnum(t *testing.T) {
	for _, family := range []string{"Inter", "POPPINS", "dejavu-sans"} {
		t.Run(family, func(t *testing.T) {
			params, _ := json.Marshal(map[string]string{"font_family": family})
			_, err := compileRuntimeStylePlan(t, string(params), `{}`)
			if err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("font family %q error = %v, want closed-enum rejection", family, err)
			}
		})
	}
}

// TestRuntimeTextStyleOverridesRejectImageLayers walks the WHOLE control set, not
// just the key that happens to be convenient: a non-text layer must reject every
// control the validator used to accept, or a new control would silently become
// image-reachable.
func TestRuntimeTextStyleOverridesRejectImageLayers(t *testing.T) {
	covered := make(map[string]bool, len(runtimeTextStyleKeys))
	for _, override := range []struct {
		key, jsonValue string
	}{
		{"font_family", `"inter"`},
		{"font_size_px", `84`},
		{"glow_size", `10`},
		{"stroke_size", `4`},
		{"shadow_blur_px", `12`},
		{"shadow_opacity", `0.6`},
		{"shadow_offset_x_px", `-3`},
		{"shadow_offset_y_px", `7`},
	} {
		covered[override.key] = true
		t.Run(override.key, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"runtime-style-image","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"image","kind":"image","template_id":"IMAGE_OVERLAY","preset_id":"image_fade_in","params":{"%s":%s},"start_ms":0,"end_ms":3000,"asset_refs":[{"asset_id":"image","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/image.png","media_type":"image/png"}]}]}`, override.key, override.jsonValue))
			if _, err := CompileSemantic(raw); err == nil || !strings.Contains(err.Error(), "non-text layer") {
				t.Fatalf("error = %v, want non-text-layer override rejection for %s", err, override.key)
			}
		})
	}
	for _, key := range runtimeTextStyleKeys {
		if !covered[key] {
			t.Errorf("runtime control %q is not exercised against a non-text layer", key)
		}
	}
}

// TestApplyTextRuntimeOverridesFailsClosedOnMalformedControls calls the lowering
// directly, past the validator, and pins the property the naive spelling did not
// have: a control that is PRESENT but malformed is an error, never a silent
// no-op. Using the second return of a bare type assertion as the "does this
// control apply" flag meant a plan carrying e.g. "glow_size":"18" rendered with
// the preset's own glow and reported success — the failure mode where a control
// disappears instead of the build stopping.
func TestApplyTextRuntimeOverridesFailsClosedOnMalformedControls(t *testing.T) {
	styleLayer := func() Layer {
		return Layer{ID: "phrase", Type: "text", Text: "Closed controls", Style: &LayerStyle{
			Font: officialFontPath, FontSize: 58,
			Glow:   &LayerGlow{Radius: 42, Intensity: 0.25, Color: "#FFFFFF"},
			Stroke: &LayerStroke{Color: "#111827", Width: 3.5},
			Shadow: &LayerShadow{Color: "#000000", Opacity: 0.72, Blur: 12, Offset: []float64{0, 4}},
		}}
	}
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"string glow size", "glow_size", "18"},
		{"bool stroke size", "stroke_size", true},
		{"string font size", "font_size_px", "84"},
		{"nil shadow blur", "shadow_blur_px", nil},
		{"string shadow opacity", "shadow_opacity", "0.6"},
		{"string shadow offset x", "shadow_offset_x_px", "-3"},
		{"map shadow offset y", "shadow_offset_y_px", map[string]any{"px": 7.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layer := styleLayer()
			err := applyTextRuntimeOverrides(&layer, map[string]any{tc.key: tc.value})
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error = %v, want a %s rejection instead of a silent no-op", err, tc.key)
			}
			if layer.Style.FontSize != 58 || layer.Style.Glow == nil || layer.Style.Stroke == nil || layer.Style.Shadow == nil {
				t.Fatalf("a rejected control changed the layer: %+v", layer.Style)
			}
		})
	}
	// Non-finite numbers are as unusable as a wrong type: NaN would lower to a
	// style the renderer cannot draw.
	layer := styleLayer()
	if err := applyTextRuntimeOverrides(&layer, map[string]any{"glow_size": math.NaN()}); err == nil {
		t.Fatal("NaN glow size was accepted")
	}
	// The documented controls still lower when they are well-formed.
	layer = styleLayer()
	if err := applyTextRuntimeOverrides(&layer, map[string]any{
		"font_family": "inter", "font_size_px": 84.0, "glow_size": 18.0, "stroke_size": 4.0,
		"shadow_blur_px": 6.0, "shadow_opacity": 0.5, "shadow_offset_x_px": -2.0, "shadow_offset_y_px": 3.0,
	}); err != nil {
		t.Fatalf("well-formed controls were rejected: %v", err)
	}
	if layer.Style.Font != "assets/fonts/Inter.ttf" || layer.Style.FontSize != 84 ||
		layer.Style.Glow == nil || layer.Style.Glow.Radius != 18 || layer.Style.Glow.Intensity != 0.75 || layer.Style.Glow.Color != "#FFB020" || layer.Style.Stroke == nil || layer.Style.Stroke.Width != 4 ||
		layer.Style.Shadow == nil || layer.Style.Shadow.Blur != 6 || layer.Style.Shadow.Opacity != 0.5 ||
		len(layer.Style.Shadow.Offset) != 2 || layer.Style.Shadow.Offset[0] != -2 || layer.Style.Shadow.Offset[1] != 3 {
		t.Fatalf("lowered style = %+v", layer.Style)
	}
}

// TestRuntimeStyleOverridesReachTheSerializedPlan is the end-to-end half of the
// silent-assertion fix: every documented control must be readable in the plan
// ARTIFACT (the JSON Chronon decodes), not only in the compiler's in-memory
// layer. A control that lowers but does not serialize would render with the
// preset's styling exactly like one that was silently dropped.
func TestRuntimeStyleOverridesReachTheSerializedPlan(t *testing.T) {
	layer, err := compileRuntimeStylePlan(t,
		`{"font_family":"inter","font_size_px":84,"glow_size":18,"stroke_size":4}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(layer)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Style struct {
			Font     string  `json:"font"`
			FontSize float64 `json:"font_size"`
			Glow     *struct {
				Radius float64 `json:"radius"`
			} `json:"glow"`
			Stroke *struct {
				Width float64 `json:"width"`
			} `json:"stroke"`
		} `json:"style"`
	}
	if err := json.Unmarshal(serialized, &decoded); err != nil {
		t.Fatalf("decode serialized layer: %v", err)
	}
	if decoded.Style.Font != "assets/fonts/Inter.ttf" || decoded.Style.FontSize != 84 {
		t.Fatalf("serialized font controls = %+v", decoded.Style)
	}
	if decoded.Style.Glow == nil || decoded.Style.Glow.Radius != 18 {
		t.Fatalf("serialized glow = %+v", decoded.Style.Glow)
	}
	if decoded.Style.Stroke == nil || decoded.Style.Stroke.Width != 4 {
		t.Fatalf("serialized stroke = %+v", decoded.Style.Stroke)
	}

	shadowLayer, err := compileRuntimeStylePlan(t,
		`{}`, `{"font_size_px":72,"shadow_blur_px":12,"shadow_opacity":0.6,"shadow_offset_x_px":-3,"shadow_offset_y_px":7}`)
	if err != nil {
		t.Fatal(err)
	}
	shadowJSON, err := json.Marshal(shadowLayer)
	if err != nil {
		t.Fatal(err)
	}
	var shadow struct {
		Style struct {
			FontSize float64 `json:"font_size"`
			Shadow   *struct {
				Opacity float64   `json:"opacity"`
				Blur    float64   `json:"blur"`
				Offset  []float64 `json:"offset"`
			} `json:"shadow"`
		} `json:"style"`
	}
	if err := json.Unmarshal(shadowJSON, &shadow); err != nil {
		t.Fatalf("decode serialized shadow layer: %v", err)
	}
	if shadow.Style.FontSize != 72 || shadow.Style.Shadow == nil || shadow.Style.Shadow.Blur != 12 || shadow.Style.Shadow.Opacity != 0.6 {
		t.Fatalf("serialized shadow controls = %+v", shadow.Style)
	}
	if len(shadow.Style.Shadow.Offset) != 2 || shadow.Style.Shadow.Offset[0] != -3 || shadow.Style.Shadow.Offset[1] != 7 {
		t.Fatalf("serialized shadow offset = %v", shadow.Style.Shadow.Offset)
	}
}
