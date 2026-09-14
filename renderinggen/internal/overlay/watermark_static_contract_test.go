package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// watermarkFontSHA is a syntactically valid content address for the font asset.
const watermarkFontSHA = "2222222222222222222222222222222222222222222222222222222222222222"

// watermarkSourceSHA is a syntactically valid content address for the source
// clip the watermark is composited over.
const watermarkSourceSHA = "3333333333333333333333333333333333333333333333333333333333333333"

// watermarkPlanJSON builds a plan whose ONLY non-video layer is the watermark,
// with an explicit clip duration so the watermark's span can be asserted
// against the canvas.
func watermarkPlanJSON(durationMS int64, style string) string {
	return fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"wm","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"duration_ms":%d,"source":{"asset_id":"src-clip","sha256":%q,"path":"assets/semantic/src-clip/source.mp4"},`+
			`"watermark":{"text":"VeloxEditing","font_ref":{"asset_id":"montserrat-bold","sha256":%q,"url":"assets/semantic/montserrat-bold/Montserrat-Bold.ttf","media_type":"font/ttf"},`+
			`"position":"top_right","opacity":0.8,"margin_px":48,"style":%s},"items":[]}`,
		durationMS, watermarkSourceSHA, watermarkFontSHA, style)
}

// TestWatermarkLayerIsStatic is the prepared-once contract. The renderer's
// static-layer analysis promotes a layer whose pixels do not change across the
// clip (no animation, no animators, no transitions, constant span) and caches
// its prepared surface, so the glyph work happens ONCE and is reused on every
// frame. That promotion is only reachable while the emitted layer keeps this
// shape — so the shape is pinned here, not left to convention. A regression
// here is invisible in output correctness and shows up only as a per-frame
// rasterisation cost on every watermarked clip.
func TestWatermarkLayerIsStatic(t *testing.T) {
	const durationMS = 8000
	raw := watermarkPlanJSON(durationMS, `{"font":"Montserrat","font_size_px":42,"color":"#FFFFFF","stroke":{"color":"#000000","width":2}}`)
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile watermark plan: %v", err)
	}
	plan := result.Plan

	var layer *Layer
	for i := range plan.Layers {
		if plan.Layers[i].ID == "watermark" {
			layer = &plan.Layers[i]
		}
	}
	if layer == nil {
		t.Fatalf("no watermark layer among %d layers", len(plan.Layers))
	}
	if layer.Type != "text" {
		t.Fatalf("watermark type = %q, want text", layer.Type)
	}
	// A static overlay must span the whole clip in ONE piece: a watermark
	// split into per-cue layers is what forced a rasterisation per cue.
	if layer.StartFrame != 0 {
		t.Errorf("watermark start frame = %d, want 0", layer.StartFrame)
	}
	if layer.DurationFrames != plan.Canvas.DurationFrames || layer.DurationFrames <= 0 {
		t.Errorf("watermark duration = %d, want the full canvas %d", layer.DurationFrames, plan.Canvas.DurationFrames)
	}
	// The three signals the renderer's static analysis reads.
	if layer.Animation != nil {
		t.Errorf("watermark carries an animation (%+v): the layer is no longer static", layer.Animation)
	}
	if len(layer.TextAnimators) != 0 {
		t.Errorf("watermark carries %d text animators: the layer is no longer static", len(layer.TextAnimators))
	}
	// The same facts on the wire, since the renderer never sees the Go struct.
	wire, err := plan.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Layers []map[string]json.RawMessage `json:"layers"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("decode marshalled plan: %v", err)
	}
	for i, layerJSON := range decoded.Layers {
		if _, ok := layerJSON["animation"]; ok {
			t.Errorf("layer %d marshals an animation key", i)
		}
		if _, ok := layerJSON["text_animators"]; ok {
			t.Errorf("layer %d marshals a text_animators key", i)
		}
	}
}

// TestWatermarkStyleTransitionIsRejected pins why the static shape above is
// guaranteed rather than merely usual: an entrance transition on the watermark
// style is refused at the contract boundary instead of being lowered into a
// per-frame animation. Without this gate a style block could silently turn the
// cheapest overlay in the clip into the most expensive one.
func TestWatermarkStyleTransitionIsRejected(t *testing.T) {
	raw := watermarkPlanJSON(8000, `{"font":"Montserrat","font_size_px":42,"color":"#FFFFFF","transition_in":{"preset":"fade","duration_ms":400}}`)
	_, err := CompileSemantic([]byte(raw))
	if err == nil {
		t.Fatal("watermark transition_in must fail closed")
	}
	if !strings.Contains(err.Error(), "transition_in") {
		t.Fatalf("error must name transition_in, got %v", err)
	}
}

// TestWatermarkRequiresResolvedLayout pins the other half of "prepared once":
// the layer's position, size, margin and typography are all resolved BEFORE the
// render, so the renderer never has to re-derive layout per frame (and never
// guesses). An unresolvable layout fails closed.
func TestWatermarkRequiresResolvedLayout(t *testing.T) {
	// margin_px missing: the worker must not invent a distance from the edge.
	raw := strings.Replace(
		watermarkPlanJSON(8000, `{"font":"Montserrat","font_size_px":42,"color":"#FFFFFF"}`),
		`"margin_px":48,`, ``, 1)
	if _, err := CompileSemantic([]byte(raw)); err == nil {
		t.Fatal("watermark without margin_px must fail closed")
	}

	// position missing: the worker must not default to a corner.
	raw = strings.Replace(
		watermarkPlanJSON(8000, `{"font":"Montserrat","font_size_px":42,"color":"#FFFFFF"}`),
		`"position":"top_right",`, ``, 1)
	if _, err := CompileSemantic([]byte(raw)); err == nil {
		t.Fatal("watermark without a position must fail closed")
	}

	// A resolved watermark carries both, plus a size the position was computed
	// against (not a size re-derived later from different inputs).
	result, err := CompileSemantic([]byte(watermarkPlanJSON(8000, `{"font":"Montserrat","font_size_px":42,"color":"#FFFFFF"}`)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, layer := range result.Plan.Layers {
		if layer.ID != "watermark" {
			continue
		}
		if len(layer.Position) != 2 || len(layer.Size) != 2 {
			t.Fatalf("watermark geometry unresolved: position=%v size=%v", layer.Position, layer.Size)
		}
		if layer.Size[0] <= 0 || layer.Size[1] <= 0 {
			t.Fatalf("watermark size %v is not a usable box", layer.Size)
		}
		if layer.Style == nil || layer.Style.Font == "" || layer.Style.FontSize <= 0 {
			t.Fatalf("watermark typography unresolved: %+v", layer.Style)
		}
		if layer.Opacity == nil || *layer.Opacity != 0.8 {
			t.Fatalf("watermark opacity not carried: %v", layer.Opacity)
		}
	}
}
