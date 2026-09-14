package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/schemaval"
)

// pipelineGenCompositePlan is the VERBATIM bytes emitted by PipelineGen's
// MapClipPlanToOverlayPlan (internal/platform/renderinggen/clip_plan_mapper.go)
// for a sealed FINAL clip that carries, at once, a materialized background, a
// text watermark and a REUSED entity overlay. It was captured from the real
// mapper output (the producer test that pins it is
// TestMapClipPlanToOverlayPlan_CompositeFinalClip on the PipelineGen side);
// every value — the overlay item id/kind/template/window/asset_refs, the
// background asset ref, the watermark text/position/margin and the Montserrat
// font_ref — is the producer's actual output, so this fixture cannot drift from
// the emitter without failing here first.
//
// This is the cross-repo half of the single-pass FINAL CLIP contract: the
// producer's real payload is accepted by THIS compiler and lowers to a
// background, a watermarked text layer and a timed video layer in ONE render
// pass, so the clip is encoded once instead of once per declared block.
const pipelineGenCompositePlan = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"composite-clip-1","video_id":"source-asset-001","width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":8000,"output_profile_id":"VELOX_ASSEMBLY_READY_V1","source":{"asset_id":"source-asset-001","path":"assets/semantic/source-asset-001/source.mp4","sha256":"879ff562919055b6ab3f48b23bf45a5007b2b8367f407ec12ccdf414120fec60"},"foreground_scale_percent":80,"background":{"kind":"video","asset_refs":[{"asset_id":"bg-asset-001","sha256":"50e412c3484b68cee372cdee124cd2421cfdd1e9bc902f108fe877155139dd80","url":"assets/semantic/bg-asset-001/background.mp4"}],"fit":"cover","loop":true},"watermark":{"text":"VeloxEditing","font_ref":{"asset_id":"font-montserrat-bold","sha256":"80395c1ec9eaca58eef2294830c944519aafeeb0ed0c7a5c4fe7826ab31f3834","url":"assets/semantic/font-montserrat-bold/Montserrat-Bold.ttf","media_type":"font/ttf"},"position":"top_right","opacity":0.85,"margin_px":48,"style":{"font":"Montserrat","color":"#FFFFFF","font_size_px":42}},"audio":{"mode":"copy_if_compatible","codec":"aac","sample_rate":48000,"channels":2},"items":[{"id":"overlay-eeeeeeeeeeeeeeee-0","kind":"video_overlay","template_id":"VIDEO_OVERLAY","motion_params":{},"text":"","start_ms":1000,"end_ms":3000,"params":{"fit":"cover"},"asset_refs":[{"asset_id":"overlay-eeeeeeeeeeeeeeee","sha256":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","url":"assets/semantic/overlay-eeeeeeeeeeeeeeee/overlay.mp4","media_type":"video/mp4"}]}]}`

// TestPipelineGenCompositePlanLowersAllThreeBlocks is the producer→worker
// boundary test for a full-fidelity FINAL clip. It pins that the producer's
// real background + text watermark + reused overlay payload lowers to:
//
//   - a background video layer (looped, cover) carrying the certified bytes;
//   - a watermarked TEXT layer with the declared copy and the font_ref;
//   - exactly one timed video layer for the reused overlay segment, top-most,
//     with the declared [1000, 3000) ms window at 24fps;
//
// all inside the single Chronon render, which is what removes the second
// transcode and lets N languages reuse ONE certified overlay render.
func TestPipelineGenCompositePlanLowersAllThreeBlocks(t *testing.T) {
	result, err := CompileSemantic([]byte(pipelineGenCompositePlan))
	if err != nil {
		t.Fatalf("PipelineGen composite plan must compile: %v", err)
	}
	if len(result.UnknownTemplates) != 0 {
		t.Fatalf("templates must resolve, got unknown: %v", result.UnknownTemplates)
	}

	layerByID := map[string]Layer{}
	for _, layer := range result.Plan.Layers {
		layerByID[layer.ID] = layer
	}

	bg, ok := layerByID["background"]
	if !ok {
		t.Fatalf("no background layer, got %v", layerIDs(result.Plan.Layers))
	}
	if bg.Type != "video" || !bg.Loop {
		t.Fatalf("background layer = type %q loop %v, want a looped video", bg.Type, bg.Loop)
	}
	if !strings.Contains(bg.Source, "bg-asset-001") {
		t.Fatalf("background source %q does not reference the declared background asset", bg.Source)
	}

	wm, ok := layerByID["watermark"]
	if !ok {
		t.Fatalf("no watermark layer, got %v", layerIDs(result.Plan.Layers))
	}
	if wm.Type != "text" || wm.Text != "VeloxEditing" {
		t.Fatalf("watermark layer = type %q text %q, want the declared text", wm.Type, wm.Text)
	}

	var overlays []Layer
	for _, layer := range result.Plan.Layers {
		if layer.Type == "video" && layer.ID != "source" && layer.ID != "background" {
			overlays = append(overlays, layer)
		}
	}
	if len(overlays) != 1 {
		t.Fatalf("want exactly one overlay video layer, got %d (%v)", len(overlays), layerIDs(result.Plan.Layers))
	}
	overlay := overlays[0]
	// 24fps → 1000ms = frame 24; 3000ms = frame 72 (exclusive end).
	if overlay.StartFrame != 24 || overlay.DurationFrames != 48 {
		t.Fatalf("overlay window = %d+%d, want 24+48 (the declared [1000, 3000) ms at 24fps)", overlay.StartFrame, overlay.DurationFrames)
	}
	if !strings.HasSuffix(overlay.Source, "overlay.mp4") || !strings.Contains(overlay.Source, "overlay-eeeeeeeeeeeeeeee") {
		t.Fatalf("overlay source %q must resolve the reused segment's asset", overlay.Source)
	}
	if last := result.Plan.Layers[len(result.Plan.Layers)-1].ID; last != overlay.ID {
		t.Fatalf("overlay must composite over the source, got top-most %q (layers %v)", last, layerIDs(result.Plan.Layers))
	}

	// The 80% foreground scale stays one centred transform on the source.
	if src, ok := layerByID["source"]; ok {
		if len(src.Scale) != 2 || src.Scale[0] != 0.8 || src.Scale[1] != 0.8 {
			t.Fatalf("source scale = %v, want [0.8 0.8] from foreground_scale_percent 80", src.Scale)
		}
	}
}

// TestPipelineGenCompositePlanIsContractValid validates the producer's real
// composite payload against the published overlay-plan.v1 schema, so a producer
// field rename can never be accepted here and rejected by a schema-validating
// deployment.
func TestPipelineGenCompositePlanIsContractValid(t *testing.T) {
	if err := schemaval.ValidateFile([]byte(pipelineGenCompositePlan), contractPresetPath(t)); err != nil {
		t.Fatalf("PipelineGen composite plan is not valid overlay-plan.v1: %v", err)
	}
}
