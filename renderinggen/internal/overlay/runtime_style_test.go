package overlay

import (
	"encoding/json"
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
	if layer.Style == nil || layer.Style.Font != "assets/fonts/Inter-Bold.ttf" {
		t.Fatalf("runtime font override = %+v", layer.Style)
	}
	if layer.Style.Glow == nil || layer.Style.Glow.Radius != 28.5 {
		t.Fatalf("runtime glow override = %+v", layer.Style.Glow)
	}
	if layer.Style.Stroke == nil || layer.Style.Stroke.Width != 5 {
		t.Fatalf("runtime stroke override = %+v", layer.Style.Stroke)
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

func TestRuntimeTextStyleOverridesRejectImageLayers(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"runtime-style-image","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"image","kind":"image","template_id":"IMAGE_OVERLAY","preset_id":"image_fade_in","params":{"glow_size":10},"start_ms":0,"end_ms":3000,"asset_refs":[{"asset_id":"image","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://example.test/image.png","media_type":"image/png"}]}]}`)
	if _, err := CompileSemantic(raw); err == nil || !strings.Contains(err.Error(), "non-text layer") {
		t.Fatalf("error = %v, want non-text-layer override rejection", err)
	}
}
