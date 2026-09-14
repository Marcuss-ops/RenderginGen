package overlay

import (
	"encoding/json"
	"fmt"
	"testing"
)

// certificationImageItem is one asset-driven certification item in the
// semantic contract. It reuses the single fixture image identity so a plan can
// carry several image items without tripping the registry's collision guard.
func certificationImageItem(id, presetID string, assetID string) string {
	return fmt.Sprintf(
		`{"id":%q,"template_id":"IMAGE_OVERLAY","preset_id":%q,"start_ms":%d,"end_ms":%d,`+
			`"asset_refs":[{"asset_id":%q,"sha256":%q,"url":"https://store.example/%s.jpg","media_type":"image/jpeg"}]}`,
		id, presetID, certificationStartMS, certificationEndMS, assetID, certificationAssetSHA, assetID)
}

func certificationTextItem(id, presetID, text string) string {
	return fmt.Sprintf(
		`{"id":%q,"template_id":"IMPORTANT_PHRASE","preset_id":%q,"text":%q,"start_ms":%d,"end_ms":%d}`,
		id, presetID, text, certificationStartMS, certificationEndMS)
}

func TestFinal_ImageAndTextTogether(t *testing.T) {
	raw := fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-plus-phrase","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"background":{"kind":"color","color":%s},"items":[%s,%s]}`,
		certificationBackgroundRGBA,
		certificationImageItem("img", "image_scale_in", "matrix-image"),
		certificationTextItem("phrase", "apple_v2", "Frase importante"))
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	plan := result.Plan
	if len(plan.Layers) != 3 {
		t.Fatalf("layers=%d, want background + image + text", len(plan.Layers))
	}
}

// TestFinal_BackgroundOpacity proves the contract's opacity is honored exactly
// as declared, including an intentional zero (never silently defaulted to 1).
func TestFinal_BackgroundOpacity(t *testing.T) {
	for _, want := range []float64{0, .25, .5, 1} {
		want := want
		t.Run(fmt.Sprintf("%.2f", want), func(t *testing.T) {
			raw := fmt.Sprintf(
				`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"opacity","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
					`"background":{"kind":"color","color":%s,"opacity":%v},"items":[%s]}`,
				certificationBackgroundRGBA, want,
				certificationTextItem("word", "apple_v2", "opacity"))
			result, err := CompileSemantic([]byte(raw))
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			plan := result.Plan
			if plan.Layers[0].Opacity == nil {
				t.Fatalf("opacity %.2f was dropped from the compiled layer (nil)", want)
			}
			if got := *plan.Layers[0].Opacity; got != want {
				t.Errorf("opacity %.2f compiled as %.2f", want, got)
			}
			// The value must survive the WIRE, not only the in-memory struct: as
			// a float64 with omitempty an explicit 0 marshalled as an ABSENT key
			// and the renderer defaulted the layer back to fully opaque — the one
			// documented meaning of 0 ("invisible") was the one unrepresentable
			// value.
			wire, err := plan.Marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded struct {
				Layers []struct {
					Opacity *float64 `json:"opacity"`
				} `json:"layers"`
			}
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatalf("decode marshalled plan: %v", err)
			}
			if len(decoded.Layers) == 0 || decoded.Layers[0].Opacity == nil {
				t.Fatalf("opacity key %.2f missing from the marshalled layer: %s", want, wire)
			}
			if got := *decoded.Layers[0].Opacity; got != want {
				t.Errorf("wire opacity %.2f, want %.2f", got, want)
			}
		})
	}
}

// TestFinal_AssetMatrix lowers a mixed background + image + text composition
// through the semantic contract: every declared layer must materialize.
func TestFinal_AssetMatrix(t *testing.T) {
	raw := fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"asset-matrix","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"background":{"kind":"video","asset_refs":[{"asset_id":"matrix-bg","sha256":%q,"url":"https://store.example/background.mp4","media_type":"video/mp4"}]},`+
			`"items":[%s,%s,%s,%s]}`,
		certificationAssetSHA,
		certificationImageItem("img-1", "image_scale_in", "matrix-a"),
		certificationImageItem("img-2", "modern_rounded_pop", "matrix-b"),
		certificationTextItem("text-1", "apple_v2", "Nome breve"),
		certificationTextItem("text-2", "apple_v2", "Frase lunga — àéìòù ✓"))
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	plan := result.Plan
	if len(plan.Layers) != 5 {
		t.Fatalf("layers=%d, want background + 4 overlays", len(plan.Layers))
	}
}
