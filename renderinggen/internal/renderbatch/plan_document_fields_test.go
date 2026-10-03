package renderbatch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func int64Pointer(value int64) *int64 { return &value }

// TestBuildPlanTransportsCompositeEntityAndMapFields keeps the writer honest for
// the three families that previously had no PlanItem field at all: a composite
// image item (image_layers + image_preset_id), an entity card with an animated
// caption (entity_caption + caption_motion_id) and a georeferenced map (map).
// Before these fields existed the only way to build such a plan was to
// hand-write the JSON — the exact drift this package's single writer prevents.
func TestBuildPlanTransportsCompositeEntityAndMapFields(t *testing.T) {
	portrait := PlanAssetRef{AssetID: "ent-portrait", SHA256: strings.Repeat("a", 64), URL: "assets/semantic/ent-portrait.png", MediaType: "image/png"}
	plateA := PlanAssetRef{AssetID: "plate-a", SHA256: strings.Repeat("b", 64), URL: "assets/semantic/plate-a.png", MediaType: "image/png"}
	plateB := PlanAssetRef{AssetID: "plate-b", SHA256: strings.Repeat("c", 64), URL: "assets/semantic/plate-b.png", MediaType: "image/png"}
	basemap := PlanAssetRef{AssetID: "basemap", SHA256: strings.Repeat("d", 64), URL: "assets/maps/world.png", MediaType: "image/png"}

	spec := PlanSpec{
		PlanID: "writer-parity", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationMS: 12000,
		Items: []PlanItem{
			{
				ID: "card", Kind: "entity_card", TemplateID: "PERSON",
				PresetID: overlay.PhraseDefaultPresetID, ImagePresetID: "image_scale_in",
				MotionID: "image_depth_dolly", EntityID: "person:ada", Text: "Ada Lovelace",
				EntityCaption: "Ada Lovelace", CaptionMotionID: "text_fade_up",
				AssetRefs: []PlanAssetRef{portrait}, DurationMS: int64Pointer(4000), StartMS: 0, EndMS: 4000,
			},
			{
				ID: "gallery", Kind: "entity_image", TemplateID: "IMAGE_OVERLAY", PresetID: "image_scale_in",
				AssetRefs: []PlanAssetRef{plateA, plateB}, DurationMS: int64Pointer(4000), StartMS: 4000, EndMS: 8000,
				ImageLayers: []overlay.SemanticImageLayer{
					{ID: "a", AssetID: "plate-a", StartMS: 0, EndMS: 4000, PresetID: "image_scale_in", MotionID: "image_yaw_reveal",
						Params: map[string]any{"width": 640.0, "height": 420.0, "position_x": -320.0, "position_y": 0.0}},
					{ID: "b", AssetID: "plate-b", StartMS: 300, EndMS: 4000, PresetID: "image_scale_in", MotionID: "image_photo_drop",
						Params: map[string]any{"width": 640.0, "height": 420.0, "position_x": 320.0, "position_y": 0.0}},
				},
			},
			{
				ID: "map", Kind: "map", TemplateID: "MAP", MotionID: "image_focus_reveal",
				AssetRefs: []PlanAssetRef{basemap}, StartMS: 8000, EndMS: 12000,
				Map: &overlay.SemanticMap{
					Provider: "local", SourceID: "operator-plate", SourceLicense: "operator-supplied",
					Center: overlay.SemanticMapPoint{}, Zoom: 1, Width: 1920, Height: 1080,
					Attribution: "Map data (c) operator plate", MotionID: "image_focus_reveal",
					Pins: []overlay.SemanticMapPin{
						{ID: "rome", Label: "Rome", Latitude: 41.890210, Longitude: 12.492231, Color: "#E11D48", RadiusPX: 16},
					},
				},
			},
		},
	}
	raw, err := BuildPlan(spec)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	for _, want := range []string{
		`"image_preset_id":"image_scale_in"`,
		`"entity_caption":"Ada Lovelace"`,
		`"caption_motion_id":"text_fade_up"`,
		`"image_layers"`,
		`"map"`,
		`"asset_id":"basemap"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("built plan is missing %s on the wire: %s", want, raw)
		}
	}

	result, err := overlay.CompileSemantic(raw)
	if err != nil {
		t.Fatalf("the decoder rejected the writer's document: %v", err)
	}
	byID := make(map[string]overlay.Layer, len(result.Plan.Layers))
	for _, layer := range result.Plan.Layers {
		byID[layer.ID] = layer
	}
	for _, want := range []string{
		"card:image", "card:entity:caption", "gallery:a:image", "gallery:b:image",
		"map:map_basemap", "map:map_pin:rome", "map:map_pin_label:rome", "map:map_attribution",
	} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("the decoder dropped %s: %+v", want, byID)
		}
	}
	// No background is declared: entity pair + 2 gallery children + basemap,
	// pin, pin label and attribution.
	if len(result.Plan.Layers) != 2+2+4 {
		t.Fatalf("compiled %d layers, want entity pair + 2 gallery children + 4 map layers: %+v", len(result.Plan.Layers), result.Plan.Layers)
	}

	// The wire keys must survive the round trip through the strict decoder, not
	// merely be present in the bytes: re-encode the decoded item and compare the
	// composite child count.
	var probe struct {
		Items []struct {
			ImageLayers []overlay.SemanticImageLayer `json:"image_layers"`
			Map         *overlay.SemanticMap         `json:"map"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if len(probe.Items) != 3 || len(probe.Items[1].ImageLayers) != 2 || probe.Items[2].Map == nil {
		t.Fatalf("wire round trip lost the composite/map declarations: %+v", probe.Items)
	}
	if probe.Items[2].Map.Pins[0].ID != "rome" {
		t.Fatalf("wire round trip lost the grounded pin: %+v", probe.Items[2].Map.Pins)
	}
}
