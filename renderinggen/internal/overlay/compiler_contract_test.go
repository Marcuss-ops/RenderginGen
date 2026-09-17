package overlay

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestCompileSemanticRejectsUntypedConcretePlan pins the fail-closed
// execution-worker boundary: a document without schema_version is rejected.
// The historical byte-for-byte pass-through let concrete plans bypass the
// compiler entirely — that bypass is the bug this test guards against.
func TestCompileSemanticRejectsUntypedConcretePlan(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan.v2","version":2,"job_id":"j","canvas":{"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"bg","type":"image","asset":"assets/background.jpg","start_frame":0,"duration_frames":150}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	result, err := CompileSemantic(raw)
	if err == nil {
		t.Fatalf("untyped concrete plan must be rejected, got compiled=%+v", result.Plan)
	}
	if len(result.Assets) != 0 {
		t.Fatalf("rejected plan must synthesize no assets, got %+v", result.Assets)
	}
	if !strings.Contains(err.Error(), "only accepted contract") {
		t.Fatalf("error must name the accepted contract, got: %v", err)
	}
}

func TestCompileSemanticOptionalBackground(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "background":{"kind":"color","color":[0,0,0,1]},
      "items":[{"id":"phrase","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","text":"hi","start_ms":0,"end_ms":1000}]
    }`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile semantic background: %v", err)
	}
	compiled := result.Plan
	if len(compiled.Layers) < 2 || compiled.Layers[0].ID != "background" || compiled.Layers[0].Type != "color" {
		t.Fatalf("background was not emitted first: %+v", compiled.Layers)
	}
	if compiled.Layers[0].DurationFrames != compiled.Canvas.DurationFrames {
		t.Fatalf("background duration=%d, canvas duration=%d", compiled.Layers[0].DurationFrames, compiled.Canvas.DurationFrames)
	}
}

func TestCompileSemanticTextMotionProducesAnimatorContract(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,
      "items":[{"id":"title","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","motion_id":"character_cascade",
        "text":"Powerfully simple.","start_ms":0,"end_ms":2000}]
    }`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile text motion: %v", err)
	}
	compiled := result.Plan
	if len(compiled.Layers) != 1 || len(compiled.Layers[0].TextAnimators) != 1 {
		t.Fatalf("expected one text animator, got %+v", compiled.Layers)
	}
	a := compiled.Layers[0].TextAnimators[0]
	if a.Selectors[0].Unit != "glyph" || len(a.Properties) != 2 {
		t.Fatalf("unexpected character cascade contract: %+v", a)
	}
	if compiled.Layers[0].Animation == nil {
		t.Fatal("important phrase must carry a layer entry/exit animation")
	}
	var opacity *AnimationTrack
	for i := range compiled.Layers[0].Animation.Tracks {
		if compiled.Layers[0].Animation.Tracks[i].Property == "opacity" {
			opacity = &compiled.Layers[0].Animation.Tracks[i]
			break
		}
	}
	if opacity == nil || len(opacity.Keyframes) != 4 {
		t.Fatalf("phrase opacity animation = %+v, want entry, hold and exit keyframes", compiled.Layers[0].Animation.Tracks)
	}
	if opacity.Keyframes[0].Frame != 0 || opacity.Keyframes[0].Value != 0.0 ||
		opacity.Keyframes[1].Frame != 8 || opacity.Keyframes[1].Value != 1.0 ||
		opacity.Keyframes[2].Frame != 39 || opacity.Keyframes[2].Value != 1.0 ||
		opacity.Keyframes[3].Frame != 47 || opacity.Keyframes[3].Value != 0.0 {
		t.Fatalf("phrase entry/exit curve = %+v, want fade-in, hold, fade-out over 48 frames", opacity.Keyframes)
	}
}

func TestCompileSemanticPhraseMotionReplacesExistingOpacityWithEntryExit(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"phrase-exit","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","motion_id":"soft_edge_spotlight_dissolve","text":"Tokyo ended the unbeaten run","start_ms":0,"end_ms":2000}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	layer := result.Plan.Layers[0]
	if layer.Animation == nil {
		t.Fatal("phrase motion must include a layer entry/exit animation")
	}
	var opacityTracks int
	for _, track := range layer.Animation.Tracks {
		if track.Property != "opacity" {
			continue
		}
		opacityTracks++
		if len(track.Keyframes) != 4 || track.Keyframes[0].Value != 0.0 || track.Keyframes[1].Value != 1.0 || track.Keyframes[2].Value != 1.0 || track.Keyframes[3].Value != 0.0 {
			t.Fatalf("phrase opacity curve = %+v, want one fade-in/hold/fade-out curve", track.Keyframes)
		}
	}
	if opacityTracks != 1 {
		t.Fatalf("phrase has %d opacity tracks, want one", opacityTracks)
	}
}

func TestCompileSemanticTextUsesExplicitCanvasLocalBox(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"placement-fixture","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,
      "items":[{"id":"title","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2",
        "motion_id":"character_cascade","text":"ABC","start_ms":0,"end_ms":5000}]
    }`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	compiled := result.Plan
	if len(compiled.Layers) != 1 {
		t.Fatalf("expected one text layer, got %d", len(compiled.Layers))
	}
	layer := compiled.Layers[0]
	if len(layer.Size) != 2 || layer.Size[0] != 1920 || layer.Size[1] != 120 {
		t.Fatalf("text local box = %#v, want [1920 120]", layer.Size)
	}
	// Chronon centers the text frame internally; the layer position is an
	// offset from the canvas center, so the canonical centered value is [0,0].
	if len(layer.Position) != 2 || layer.Position[0] != 0 || layer.Position[1] != 0 {
		t.Fatalf("centered text position = %#v, want [0 0]", layer.Position)
	}
	if len(layer.TextAnimators) != 1 || layer.TextAnimators[0].Selectors[0].Unit != "glyph" {
		t.Fatalf("ABC selector fixture was not transported: %#v", layer.TextAnimators)
	}
}

func TestCompileSemanticTextMotionsDoNotCollapseToSameContract(t *testing.T) {
	motions := []string{"word_reveal", "character_cascade", "opacity_wave", "scale_wave", "char_wave"}
	contracts := make(map[string]string, len(motions))
	for _, motionID := range motions {
		raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"` + motionID + `","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"title","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","motion_id":"` + motionID + `","text":"ABC","start_ms":0,"end_ms":5000}]}`)
		result, err := CompileSemantic(raw)
		if err != nil {
			t.Fatalf("%s: %v", motionID, err)
		}
		compiled := result.Plan
		if len(compiled.Layers) != 1 || len(compiled.Layers[0].TextAnimators) != 1 {
			t.Fatalf("%s: missing text animator", motionID)
		}
		data, err := json.Marshal(compiled.Layers[0].TextAnimators[0])
		if err != nil {
			t.Fatal(err)
		}
		contract := string(data)
		if previous, exists := contracts[contract]; exists {
			t.Fatalf("%s collapsed to the same text animator contract as %s", motionID, previous)
		}
		contracts[contract] = motionID
	}
}

// TestCompileSemanticRejectsMissingPresetID pins ADR-029 forward-point (d):
// RenderingGen no longer re-maps a template_id to a preset (it must not know
// that IMPORTANT_PHRASE means a particular visual preset). A preset-driven template without
// a preset_id is rejected — the semantic_role → preset decision lives only in
// PipelineGen's SemanticOverlayResolver.
func TestCompileSemanticRejectsMissingPresetID(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","template_id":"IMPORTANT_PHRASE","text":"hi","start_ms":0,"end_ms":1000},
        {"id":"word","template_id":"IMPORTANT_WORD","text":"APPLE","start_ms":1000,"end_ms":2000}
      ]
    }`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("preset-driven template without preset_id must be rejected (no template→preset mirror)")
	}
}

// TestCompileSemanticPresetLessPrimitiveCompilesBare pins that preset-less
// primitives (PRODUCT / LOGO) do NOT require a preset_id: they compile to a
// bare layer whose appearance is the renderer's default. This is the other
// half of ADR-029 (d) — RenderingGen only enforces the preset for preset-driven
// templates, never invents one for a primitive.
func TestCompileSemanticPresetLessPrimitiveCompilesBare(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"product","template_id":"PRODUCT","start_ms":0,"end_ms":1000,
         "asset_refs":[{"asset_id":"prod","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://store.example/objects/prod.png","media_type":"image/png"}]}
      ]
    }`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("preset-less primitive must compile: %v", err)
	}
	if len(result.Plan.Layers) != 1 {
		t.Fatalf("preset-less primitive must compile to a bare layer: %+v", result.Plan.Layers)
	}
}

// TestCompileSemanticRejectsUnknownSchema pins the fail-closed path for a
// plan that is neither concrete nor the known semantic contract.
func TestCompileSemanticRejectsUnknownSchema(t *testing.T) {
	raw := []byte(`{"schema_version":"something.else.v9","plan_id":"p"}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("unknown plan schema must be rejected")
	}
}

// TestCompileSemanticMalformedJSON pins graceful decoding failure.
func TestCompileSemanticMalformedJSON(t *testing.T) {
	if _, err := CompileSemantic([]byte(`{not-json`)); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}

// TestCompileSemanticKindAndExplicitText pins the kind-SSOT contract: the
// plan's preset_id slot is compiled through the SAME single compileSemantic
// path (no new renderer), and the display text comes ONLY from the item's
// explicit `text` — RenderingGen has no entity_ref fallback.
func TestCompileSemanticKindAndExplicitText(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","text":"QUESTO CAMBIA TUTTO","start_ms":0,"end_ms":1000},
        {"id":"person","entity_id":"person:cook","kind":"entity_card","template_id":"PERSON","preset_id":"apple_v2","text":"Cook","start_ms":1000,"end_ms":2000,"duration_ms":1000}
      ]
    }`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("semantic plan must compile: %v", err)
	}
	compiled := result.Plan
	if len(compiled.Layers) != 2 {
		t.Fatalf("compiled layers = %+v", compiled.Layers)
	}
	// The item's explicit text is used verbatim; RenderingGen never invents a
	// display text.
	if compiled.Layers[1].Text != "Cook" {
		t.Fatalf("entity text = %q, want Cook", compiled.Layers[1].Text)
	}
	if compiled.Layers[1].Animation == nil || len(compiled.Layers[1].Animation.Tracks) == 0 {
		t.Fatalf("expected generic animation tracks, got %+v", compiled.Layers[1].Animation)
	}
}

// TestCompileSemanticTextIsMandatory pins that a text kind without explicit
// text is rejected: RenderingGen never reconstructs a display name from
// entity_ref or any other fallback — PipelineGen owns the displayed text.
func TestCompileSemanticTextIsMandatory(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"person","entity_id":"person:missing-text","kind":"entity_card","template_id":"PERSON","preset_id":"apple_v2","start_ms":0,"end_ms":1000,"duration_ms":1000}
      ]
    }`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("text kind without text must be rejected (no entity_ref fallback)")
	}
}

// TestCompileSemanticImportantPhraseAndEntityImage pins the two overlay
// classes used by the first real Chronon canary together. IMPORTANT_PHRASE is
// a readable emphasis card; PERSON + image_preset is an image-only entity
// composition where the name remains producer metadata and the image is the
// sole rendered layer.
func TestCompileSemanticImportantPhraseAndEntityImage(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"phrase-and-name","video_id":"phrase-and-name",
      "width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase-important","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2",
         "text":"THIS CHANGES EVERYTHING","start_ms":500,"end_ms":1800},
        {"id":"person-image-name","entity_id":"person:matt-damon","kind":"entity_card","template_id":"PERSON","preset_id":"apple_v2","image_preset_id":"image_slide_left",
         "text":"Matt Damon",
         "start_ms":2200,"end_ms":4200,"duration_ms":2000,
         "asset_refs":[{"asset_id":"matt-damon","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                         "url":"https://store.example/objects/matt-damon.png","media_type":"image/png"}]}
      ]
    }`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("semantic phrase/name plan: %v", err)
	}
	compiled, assets := result.Plan, result.Assets
	if len(compiled.Layers) != 2 {
		t.Fatalf("compiled layers = %+v", compiled.Layers)
	}
	if compiled.Layers[0].Text != "THIS CHANGES EVERYTHING" {
		t.Fatalf("important phrase layer = %+v", compiled.Layers[0])
	}
	if compiled.Layers[1].Type != "image" || compiled.Layers[1].Asset != "assets/semantic/matt-damon.png" {
		t.Fatalf("named image asset layer = %+v", compiled.Layers[1])
	}
	if compiled.Layers[1].Animation == nil || len(compiled.Layers[1].Animation.Tracks) == 0 {
		t.Fatalf("entity image must carry image preset animation = %+v", compiled.Layers[1].Animation)
	}
	// Entity portraits remain at the preset's resolved 480px size, but the
	// subject itself is centered in the composition and fully contained.
	if len(compiled.Layers[1].Position) != 2 || compiled.Layers[1].Position[0] != 0 || compiled.Layers[1].Position[1] != 0 {
		t.Fatalf("entity image must be centered = %+v", compiled.Layers[1].Position)
	}
	if compiled.Layers[1].BoxWidth != 480 || compiled.Layers[1].BoxHeight != 480 {
		t.Fatalf("entity image size changed = %dx%d", compiled.Layers[1].BoxWidth, compiled.Layers[1].BoxHeight)
	}
	if compiled.Layers[1].Fit != "contain" {
		t.Fatalf("entity image must not crop = %q", compiled.Layers[1].Fit)
	}
	if compiled.Layers[1].Text != "" {
		t.Fatalf("entity image must not compile a name layer = %+v", compiled.Layers[1])
	}
	if len(assets) != 1 || assets[0].LogicalPath != "assets/semantic/matt-damon.png" {
		t.Fatalf("materialized assets = %+v", assets)
	}
}

// TestCompileSemanticEntityImagePopupKeepsPresetMotionAndCenters verifies the
// production path emitted by PipelineGen after a verified entity image is
// attached. It must keep the official preset animation while centering the
// image-only layer; the legacy entity_card adapter above is not this path.
func TestCompileSemanticEntityImagePopupKeepsPresetMotionAndCenters(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"entity-image-popup","video_id":"entity-image-popup",
      "width":1920,"height":1080,"fps_num":24,"fps_den":1,
      "items":[
        {"id":"jordan-image","entity_id":"person:michael-jordan","kind":"entity_image","template_id":"image_popup","preset_id":"image_slide_right",
         "start_ms":0,"end_ms":5000,
         "asset_refs":[{"asset_id":"jordan","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                         "url":"https://store.example/objects/jordan.jpg","media_type":"image/jpeg"}]}
      ]
    }`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("entity image popup plan: %v", err)
	}
	if len(result.Plan.Layers) != 1 {
		t.Fatalf("compiled layers = %+v", result.Plan.Layers)
	}
	layer := result.Plan.Layers[0]
	if layer.Type != "image" || layer.Asset != "assets/semantic/jordan.jpg" {
		t.Fatalf("entity image popup layer = %+v", layer)
	}
	if layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
		t.Fatalf("official image preset animation was dropped = %+v", layer.Animation)
	}
	if len(layer.Position) != 2 || layer.Position[0] != 0 || layer.Position[1] != 0 {
		t.Fatalf("entity image popup must be centered = %+v", layer.Position)
	}
	if layer.BoxWidth != 480 || layer.BoxHeight != 480 || layer.Fit != "contain" {
		t.Fatalf("entity image popup geometry changed = %dx%d fit=%q", layer.BoxWidth, layer.BoxHeight, layer.Fit)
	}
}

// TestCompileSemanticEntityImageKeepsEveryGeneratedPresetMotion pins the whole
// preset set that can reach a generated entity image. PipelineGen's
// imagePresetCandidates (internal/capabilities/overlays/preset_selection.go) is
// the only selector for the generated overlay path, so every candidate it can
// emit must lower to a CENTERED image layer that keeps that preset's own
// official motion. RenderingGen never invents a preset, a layer or a track: the
// lowered animation must be exactly animationForDefinition(preset).
func TestCompileSemanticEntityImageKeepsEveryGeneratedPresetMotion(t *testing.T) {
	// Keep in lockstep with PipelineGen's imagePresetCandidates.
	generated := []string{"image_fast_fade", "image_slide_left", "image_slide_right", "bottom_card_rise"}
	for _, presetID := range generated {
		t.Run(presetID, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{
              "schema_version":"renderinggen.overlay-plan.v1",
              "plan_id":"entity-image-%[1]s","video_id":"entity-image-%[1]s",
              "width":1920,"height":1080,"fps_num":24,"fps_den":1,
              "items":[
                {"id":"jordan-%[1]s","entity_id":"person:michael-jordan","kind":"entity_image","template_id":"image_popup","preset_id":"%[1]s",
                 "start_ms":0,"end_ms":5000,
                 "asset_refs":[{"asset_id":"jordan","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                                 "url":"https://store.example/objects/jordan.jpg","media_type":"image/jpeg"}]}
              ]
            }`, presetID))
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("entity image plan with %s: %v", presetID, err)
			}
			if len(result.Plan.Layers) != 1 {
				t.Fatalf("compiled layers = %+v", result.Plan.Layers)
			}
			layer := result.Plan.Layers[0]
			if layer.Type != "image" || layer.Asset != "assets/semantic/jordan.jpg" {
				t.Fatalf("entity image layer = %+v", layer)
			}
			// The portrait is the sole rendered layer: the entity name stays
			// producer metadata.
			if layer.Text != "" {
				t.Fatalf("entity image must not compile a name layer = %q", layer.Text)
			}
			// Geometry is the official image preset's own 480x480 contain box,
			// untouched by the centering rule.
			if layer.BoxWidth != 480 || layer.BoxHeight != 480 || layer.Fit != "contain" {
				t.Fatalf("entity image geometry changed = %dx%d fit=%q", layer.BoxWidth, layer.BoxHeight, layer.Fit)
			}
			// Image anchors (image_right / image_left / bottom_right) must never
			// push the portrait off-canvas: the base position is the center.
			if len(layer.Position) != 2 || layer.Position[0] != 0 || layer.Position[1] != 0 {
				t.Fatalf("entity image must be centered = %+v", layer.Position)
			}
			// The motion must be the official preset's own lowering, not an
			// invented opacity/scale track.
			//
			// The lowering is asked for with the SAME duration the compiler
			// used. An official image preset owns an entrance AND an exit window
			// (imagePresetExit), and the exit only materialises when the layer's
			// duration is known — so a duration-less lowering would expect an
			// enter-only motion and reject the official exit as "invented". The
			// comparison stays exact (reflect.DeepEqual on the whole animation),
			// which is what makes it a guard against invented tracks rather than
			// a restatement of the input.
			def, err := ResolveOfficialPreset(presetID)
			if err != nil {
				t.Fatalf("resolve official preset %s: %v", presetID, err)
			}
			want, err := animationForPreset(def, "", layer.DurationFrames)
			if err != nil {
				t.Fatalf("lower official preset %s motion: %v", presetID, err)
			}
			if want == nil || len(want.Tracks) == 0 {
				t.Fatalf("official preset %s must own a motion", presetID)
			}
			if !reflect.DeepEqual(layer.Animation, want) {
				t.Fatalf("entity image animation is not the official %s motion\n got: %+v\nwant: %+v", presetID, layer.Animation, want)
			}
			t.Logf("preset=%s anchor=%s motion=%s box=%dx%d position=%v tracks=%+v",
				presetID, def.Layout.Anchor, def.Motion.ID, layer.BoxWidth, layer.BoxHeight, layer.Position, layer.Animation.Tracks)
		})
	}
}

// TestCompileSemanticTransportsChrononPresetID ensures RenderingGen does
// not mirror Chronon's preset registry. Chronon remains responsible for the
// authoritative lookup during plan compilation.
func TestCompileSemanticTransportsChrononPresetID(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","text":"x","start_ms":0,"end_ms":1000}
      ]
    }`)
	if _, err := CompileSemantic(raw); err != nil {
		t.Fatalf("non-empty Chronon preset id must be transported: %v", err)
	}
}
