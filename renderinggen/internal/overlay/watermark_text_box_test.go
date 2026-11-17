package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// textWatermarkPlanJSON builds a plan whose only non-video layer is a TEXT
// watermark with the given copy and style, so the derived box can be asserted
// independently of image watermarks.
func textWatermarkPlanJSON(text, style string) string {
	quoted, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"wm-text-box","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"duration_ms":8000,"source":{"asset_id":"src-clip","sha256":%q,"path":"assets/semantic/src-clip/source.mp4"},`+
			`"watermark":{"text":%s,"font_ref":{"asset_id":"montserrat-bold","sha256":%q,"url":"assets/semantic/montserrat-bold/Montserrat-Bold.ttf","media_type":"font/ttf"},`+
			`"position":"top_right","opacity":0.8,"margin_px":36,"style":%s},"items":[]}`,
		watermarkSourceSHA, quoted, watermarkFontSHA, style)
}

func compiledTextWatermarkLayer(t *testing.T, text, style string) *Layer {
	t.Helper()
	result, err := CompileSemantic([]byte(textWatermarkPlanJSON(text, style)))
	if err != nil {
		t.Fatalf("compile text watermark plan: %v", err)
	}
	for i := range result.Plan.Layers {
		if result.Plan.Layers[i].ID == "watermark" {
			return &result.Plan.Layers[i]
		}
	}
	t.Fatalf("no watermark layer among %d layers", len(result.Plan.Layers))
	return nil
}

// TestTextWatermarkBoxFitsTheDeclaredText guards the regression that shipped
// with the fixed canvas/6 box: "OBAMA ARCHIVIO" at 34 px is wider than one
// sixth of a 1920 px canvas, so the engine wrapped it and the final "O" was
// clipped on the second line.
func TestTextWatermarkBoxFitsTheDeclaredText(t *testing.T) {
	const text = "OBAMA ARCHIVIO"
	const margin = 36.0
	const canvasW = 1920.0
	layer := compiledTextWatermarkLayer(t, text, `{"font":"Montserrat","font_size_px":34,"color":"#FFFFFF","stroke":{"color":"#000000","width":3},"shadow":{"color":"#000000","opacity":0.95,"blur_px":5,"offset_x":2,"offset_y":3}}`)

	if strings.Contains(layer.Text, "\n") {
		t.Fatalf("watermark text was wrapped at compile time: %q", layer.Text)
	}
	if layer.Size[0] <= canvasW/WatermarkBoxWidthDivisor {
		t.Fatalf("watermark box width = %g, want wider than the nominal canvas/6 (%g) so the full string fits", layer.Size[0], canvasW/WatermarkBoxWidthDivisor)
	}
	left := layer.Position[0] - layer.Size[0]/2
	right := layer.Position[0] + layer.Size[0]/2
	if left < margin-0.5 || right > canvasW-margin+0.5 {
		t.Fatalf("watermark box spans [%g,%g], want it inside the declared %g px margin", left, right, margin)
	}
}

// TestTextWatermarkBoxGrowsVerticallyWhenClamped pins the fallback for text
// that cannot fit on one line even at full canvas width: the width is clamped
// to the available area and the height grows with the estimated wrapped line
// count, so no line is clipped vertically.
func TestTextWatermarkBoxGrowsVerticallyWhenClamped(t *testing.T) {
	const margin = 36.0
	const canvasW = 1920.0
	text := strings.Repeat("MICHELLE OBAMA ARCHIVIO PRESIDENZIALE ", 8)
	layer := compiledTextWatermarkLayer(t, text, `{"font":"Montserrat","font_size_px":34,"color":"#FFFFFF","stroke":{"color":"#000000","width":2}}`)

	if layer.Size[0] > canvasW-2*margin+0.5 {
		t.Fatalf("watermark box width = %g, want it clamped to the available %g px", layer.Size[0], canvasW-2*margin)
	}
	if layer.Size[1] <= DefaultWatermarkBoxHeight {
		t.Fatalf("watermark box height = %g, want it to grow beyond %g for a wrapped string", layer.Size[1], DefaultWatermarkBoxHeight)
	}
}

// TestTextWatermarkExplicitBoxWins keeps the producer in charge: an explicit
// width_px/height_px pair is never re-derived from the text.
func TestTextWatermarkExplicitBoxWins(t *testing.T) {
	layer := compiledTextWatermarkLayer(t, "OBAMA ARCHIVIO", `{"font":"Montserrat","font_size_px":34,"color":"#FFFFFF","width_px":600,"height_px":72}`)
	if len(layer.Size) != 2 || layer.Size[0] != 600 || layer.Size[1] != 72 {
		t.Fatalf("watermark size = %v, want the explicit [600 72]", layer.Size)
	}
}
