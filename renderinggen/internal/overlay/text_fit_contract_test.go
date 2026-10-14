package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEnforceTextFitContractPinsShrinkOnlyWhenLimitsPresent pins the compiler
// choke point: any style that carries font size limits must leave with a
// non-none fit mode, because Chronon rejects the contradictory payload during
// plan preparation ("text font size limits require a non-none fit_mode").
func TestEnforceTextFitContractPinsShrinkOnlyWhenLimitsPresent(t *testing.T) {
	cases := []struct {
		name  string
		style LayerStyle
		want  string
	}{
		{
			name:  "limits without a fit mode become shrink_only",
			style: LayerStyle{FontSize: 42, MinFontSize: 42, MaxFontSize: 42},
			want:  FitModeShrinkOnly,
		},
		{
			name:  "an explicit none cannot keep limits",
			style: LayerStyle{FontSize: 42, FitMode: "none", MinFontSize: 40, MaxFontSize: 42},
			want:  FitModeShrinkOnly,
		},
		{
			name:  "an authored fit mode survives",
			style: LayerStyle{FontSize: 42, FitMode: "grow_and_shrink", MinFontSize: 24, MaxFontSize: 64},
			want:  "grow_and_shrink",
		},
		{
			name:  "a style without limits is untouched",
			style: LayerStyle{FontSize: 42},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			style := tc.style
			enforceTextFitContract(&style)
			if style.FitMode != tc.want {
				t.Fatalf("fit_mode = %q, want %q (style %+v)", style.FitMode, tc.want, style)
			}
			if tc.want == "" && (style.MinFontSize != 0 || style.MaxFontSize != 0) {
				t.Fatalf("limit-free style gained limits: %+v", style)
			}
		})
	}
}

// TestCompiledTextLayersSatisfyChrononFitContract asserts the invariant the
// Chronon decoder enforces on every emitted text layer, not just the styles a
// role resolver touches directly: limits must be positive, ordered and paired
// with a non-none fit mode.
func TestCompiledTextLayersSatisfyChrononFitContract(t *testing.T) {
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1", "plan_id": "fit-contract", "video_id": "v",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"items": []any{
			map[string]any{
				"id": "clause", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
				"preset_id": "phrase_default", "text": "Un'affermazione chiave molto lunga",
				"start_ms": 0, "end_ms": 5000, "duration_ms": 5000,
			},
			map[string]any{
				"id": "portrait", "entity_id": "place:friuli-venezia-giulia", "kind": "entity_image",
				"template_id": "image_popup", "preset_id": "image_scale_in", "image_preset_id": "image_scale_in",
				"entity_caption": "Friuli Venezia Giulia", "entity_style_id": "01_entity_pitch_lift_text_below",
				"start_ms": 0, "end_ms": 5000,
				"asset_refs": []any{map[string]any{
					"asset_id": "portrait", "sha256": strings.Repeat("a", 64),
					"url": "https://store.example/portrait.png", "media_type": "image/png",
				}},
			},
		},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic fit-contract plan: %v", err)
	}
	texts := 0
	for _, layer := range result.Plan.Layers {
		if layer.Type != "text" || layer.Style == nil {
			continue
		}
		texts++
		style := layer.Style
		if style.MinFontSize <= 0 && style.MaxFontSize <= 0 {
			continue
		}
		if style.MinFontSize <= 0 || style.MaxFontSize <= 0 || style.MaxFontSize < style.MinFontSize {
			t.Errorf("layer %q has a malformed fit interval: min=%v max=%v", layer.ID, style.MinFontSize, style.MaxFontSize)
		}
		if style.FitMode == "" || style.FitMode == "none" {
			t.Errorf("layer %q declares limits %v/%v with fit_mode %q; Chronon rejects that payload",
				layer.ID, style.MinFontSize, style.MaxFontSize, style.FitMode)
		}
	}
	if texts == 0 {
		t.Fatal("fit-contract plan compiled no text layers to verify")
	}
}
