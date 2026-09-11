package overlay

import (
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
		certificationTextItem("phrase", "phrase_fade_in", "Frase importante"))
	plan, _, semantic, err := CompileIfSemantic([]byte(raw))
	if err != nil || !semantic {
		t.Fatalf("semantic=%v err=%v", semantic, err)
	}
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
				certificationTextItem("word", "active_word_pop", "opacity"))
			plan, _, semantic, err := CompileIfSemantic([]byte(raw))
			if err != nil || !semantic {
				t.Fatalf("semantic=%v err=%v", semantic, err)
			}
			if got := plan.Layers[0].Opacity; got != want {
				t.Errorf("opacity %.2f compiled as %.2f", want, got)
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
		certificationTextItem("text-1", "lower_third_safe", "Nome breve"),
		certificationTextItem("text-2", "phrase_fade_in", "Frase lunga — àéìòù ✓"))
	plan, _, semantic, err := CompileIfSemantic([]byte(raw))
	if err != nil || !semantic {
		t.Fatalf("semantic=%v err=%v", semantic, err)
	}
	if len(plan.Layers) != 5 {
		t.Fatalf("layers=%d, want background + 4 overlays", len(plan.Layers))
	}
}
