package renderbatch

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// runtimeStylePlan is a one-item phrase plan carrying the documented runtime
// controls in both spellings: Params for the items a corpus sets broadly, Style
// for the item-local override block that wins on a shared key.
func runtimeStylePlan() PlanSpec {
	return PlanSpec{
		PlanID: "runtime-style", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationMS: 5000,
		Background: &Surface{Kind: "color", Color: []float64{0, 0, 0, 1}},
		Items: []PlanItem{{
			ID: "phrase", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE",
			PresetID: overlay.PhraseDefaultPresetID, Text: "Runtime style",
			StartMS: 0, EndMS: 5000,
			Params: map[string]any{"glow_size": 18.0, "stroke_size": 4.0, "font_family": "inter"},
			Style: map[string]any{
				"font_size_px": 84.0, "shadow_blur_px": 12.0,
				"shadow_opacity": 0.6, "shadow_offset_x_px": -3.0, "shadow_offset_y_px": 7.0,
			},
		}},
	}
}

// TestBuildPlanTransportsRuntimeStyleControls keeps the payload path honest. The
// runtime controls (font family/size, glow, stroke, shadow) were documented and
// decoded by the compiler but the batch writer had no field to carry them, so the
// only way to select one was to hand-write the plan — the exact drift this
// package's single writer exists to prevent.
func TestBuildPlanTransportsRuntimeStyleControls(t *testing.T) {
	raw, err := BuildPlan(runtimeStylePlan())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var semantic struct {
		Items []struct {
			Params map[string]any `json:"params"`
			Style  map[string]any `json:"style"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &semantic); err != nil {
		t.Fatalf("decode semantic plan: %v", err)
	}
	if len(semantic.Items) != 1 {
		t.Fatalf("semantic items = %d, want 1", len(semantic.Items))
	}
	for key, want := range map[string]any{
		"glow_size": 18.0, "stroke_size": 4.0, "font_family": "inter",
	} {
		if semantic.Items[0].Params[key] != want {
			t.Errorf("params[%q] = %v, want %v", key, semantic.Items[0].Params[key], want)
		}
	}
	if semantic.Items[0].Style["font_size_px"] != 84.0 {
		t.Errorf("style.font_size_px = %v, want 84", semantic.Items[0].Style["font_size_px"])
	}

	// The lowering, not just the transport: a control that survives the writer
	// but never reaches the concrete plan would still render with the preset's
	// own styling.
	concrete, err := CompileRenderPlan(raw)
	if err != nil {
		t.Fatalf("CompileRenderPlan: %v", err)
	}
	var plan struct {
		Layers []struct {
			Style *struct {
				FontSize float64 `json:"font_size"`
				Glow     *struct {
					Radius float64 `json:"radius"`
				} `json:"glow"`
				Shadow *struct {
					Opacity float64   `json:"opacity"`
					Blur    float64   `json:"blur"`
					Offset  []float64 `json:"offset"`
				} `json:"shadow"`
			} `json:"style"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(concrete, &plan); err != nil {
		t.Fatalf("decode concrete plan: %v", err)
	}
	styled := false
	for _, layer := range plan.Layers {
		if layer.Style == nil || layer.Style.FontSize != 84 {
			continue
		}
		styled = true
		if layer.Style.Glow == nil || layer.Style.Glow.Radius != 18 {
			t.Errorf("glow = %+v, want radius 18", layer.Style.Glow)
		}
		shadow := layer.Style.Shadow
		if shadow == nil || shadow.Blur != 12 || shadow.Opacity != 0.6 {
			t.Fatalf("shadow = %+v, want blur 12 / opacity 0.6", shadow)
		}
		if len(shadow.Offset) != 2 || shadow.Offset[0] != -3 || shadow.Offset[1] != 7 {
			t.Errorf("shadow offset = %v, want [-3 7]", shadow.Offset)
		}
	}
	if !styled {
		t.Fatal("no layer carries the requested font size 84: the runtime controls never reached the concrete plan")
	}
}

// TestBuildPlanRejectsUnsupportedRuntimeStyle locks the build-time failure. The
// worker's closed runtime contract is enforced inside BuildPlan, so a producer
// naming a font family or a size the compiler cannot lower is told at build time
// rather than after GPU time has been committed.
func TestBuildPlanRejectsUnsupportedRuntimeStyle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		style map[string]any
	}{
		{"unknown font family", map[string]any{"font_family": "/tmp/unsafe.ttf"}},
		{"oversized font size", map[string]any{"font_size_px": 513.0}},
		{"oversized shadow blur", map[string]any{"shadow_blur_px": 257.0}},
		{"shadow opacity above one", map[string]any{"shadow_opacity": 1.5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := runtimeStylePlan()
			spec.Items[0].Params = nil
			spec.Items[0].Style = tc.style
			if _, err := BuildPlan(spec); err == nil {
				t.Fatalf("BuildPlan accepted %v", tc.style)
			}
		})
	}
}
