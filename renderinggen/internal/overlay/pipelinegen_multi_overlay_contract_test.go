package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/schemaval"
)

// pipelineGenMultiOverlayPlan is the VERBATIM bytes emitted by PipelineGen's
// MapClipPlanToOverlayPlan for a sealed clip that composites TWO certified
// overlay.render artifacts — the shape production actually produces, because
// the overlay renderer emits one short video per semantic item
// (separateItemRenders). It was captured from the real mapper output (the
// producer test that pins it is TestMapClipPlanToOverlayPlan_CompositeFinalClip
// plus the multi-segment case in clip_overlay_mapper_test.go).
//
// Modelling the overlay as a single segment silently dropped every item after
// the first; this fixture is the cross-repo half of the list contract: the
// producer's real two-segment payload is accepted by THIS compiler and lowers
// to TWO timed video layers in the one render pass.
const pipelineGenMultiOverlayPlan = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"composite-clip-1","video_id":"source-asset-001","width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":8000,"output_profile_id":"VELOX_ASSEMBLY_READY_V1","source":{"asset_id":"source-asset-001","path":"assets/semantic/source-asset-001/source.mp4","sha256":"879ff562919055b6ab3f48b23bf45a5007b2b8367f407ec12ccdf414120fec60"},"foreground_scale_percent":80,"background":{"kind":"video","asset_refs":[{"asset_id":"bg-asset-001","sha256":"50e412c3484b68cee372cdee124cd2421cfdd1e9bc902f108fe877155139dd80","url":"assets/semantic/bg-asset-001/background.mp4"}],"fit":"cover","loop":true},"watermark":{"text":"VeloxEditing","font_ref":{"asset_id":"font-montserrat-bold","sha256":"80395c1ec9eaca58eef2294830c944519aafeeb0ed0c7a5c4fe7826ab31f3834","url":"assets/semantic/font-montserrat-bold/Montserrat-Bold.ttf","media_type":"font/ttf"},"position":"top_right","opacity":0.85,"margin_px":48,"style":{"font":"Montserrat","color":"#FFFFFF","font_size_px":42}},"audio":{"mode":"copy_if_compatible","codec":"aac","sample_rate":48000,"channels":2},"items":[{"id":"overlay-eeeeeeeeeeeeeeee-0","kind":"video_overlay","template_id":"VIDEO_OVERLAY","motion_params":{},"text":"","start_ms":1000,"end_ms":3000,"params":{"fit":"cover"},"asset_refs":[{"asset_id":"overlay-eeeeeeeeeeeeeeee","sha256":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","url":"assets/semantic/overlay-eeeeeeeeeeeeeeee/overlay.mp4","media_type":"video/mp4"}]},{"id":"overlay-1111111111111111-1","kind":"video_overlay","template_id":"VIDEO_OVERLAY","motion_params":{},"text":"","start_ms":4000,"end_ms":6500,"params":{"fit":"cover"},"asset_refs":[{"asset_id":"overlay-1111111111111111","sha256":"1111111111111111111111111111111111111111111111111111111111111111","url":"assets/semantic/overlay-1111111111111111/overlay.mp4","media_type":"video/mp4"}]}]}`

// TestPipelineGenMultiOverlayPlanLowersEverySegment is the producer→worker
// boundary test for a clip that composites MORE THAN ONE overlay. It pins that
// every declared item lowers to its own timed video layer with its own window
// and its own content-addressed source, in the single Chronon render — so a
// scene carrying a phrase AND an entity card keeps both instead of losing the
// second.
func TestPipelineGenMultiOverlayPlanLowersEverySegment(t *testing.T) {
	result, err := CompileSemantic([]byte(pipelineGenMultiOverlayPlan))
	if err != nil {
		t.Fatalf("PipelineGen multi-overlay plan must compile: %v", err)
	}
	if len(result.UnknownTemplates) != 0 {
		t.Fatalf("VIDEO_OVERLAY must be a registered template, got unknown: %v", result.UnknownTemplates)
	}

	var overlays []Layer
	for _, layer := range result.Plan.Layers {
		if layer.Type == "video" && layer.ID != "source" && layer.ID != "background" {
			overlays = append(overlays, layer)
		}
	}
	if len(overlays) != 2 {
		t.Fatalf("want two overlay video layers, got %d (%v)", len(overlays), layerIDs(result.Plan.Layers))
	}
	if overlays[0].ID == overlays[1].ID {
		t.Fatalf("overlay layers must have distinct ids, both were %q", overlays[0].ID)
	}
	// 24fps → [1000,3000) ms = 24+48 frames; [4000,6500) ms = 96+60 frames.
	if overlays[0].StartFrame != 24 || overlays[0].DurationFrames != 48 {
		t.Fatalf("first overlay window = %d+%d, want 24+48", overlays[0].StartFrame, overlays[0].DurationFrames)
	}
	if overlays[1].StartFrame != 96 || overlays[1].DurationFrames != 60 {
		t.Fatalf("second overlay window = %d+%d, want 96+60", overlays[1].StartFrame, overlays[1].DurationFrames)
	}
	if !strings.Contains(overlays[0].Source, "overlay-eeeeeeeeeeeeeeee") {
		t.Fatalf("first overlay source %q does not reference its own segment", overlays[0].Source)
	}
	if !strings.Contains(overlays[1].Source, "overlay-1111111111111111") {
		t.Fatalf("second overlay source %q does not reference its own segment", overlays[1].Source)
	}

	// Both segments must ship as content-addressed manifest assets, or the
	// worker cannot materialize the second one and the render fails.
	hashes := map[string]bool{}
	for _, asset := range result.Assets {
		hashes[asset.Hash] = true
	}
	for _, want := range []string{strings.Repeat("e", 64), strings.Repeat("1", 64)} {
		if !hashes[want] {
			t.Fatalf("segment %s missing from the asset manifest: %+v", want[:12], result.Assets)
		}
	}
}

// TestPipelineGenMultiOverlayPlanIsContractValid validates the producer's real
// two-segment payload against the published overlay-plan.v1 schema.
func TestPipelineGenMultiOverlayPlanIsContractValid(t *testing.T) {
	if err := schemaval.ValidateFile([]byte(pipelineGenMultiOverlayPlan), contractPresetPath(t)); err != nil {
		t.Fatalf("PipelineGen multi-overlay plan is not valid overlay-plan.v1: %v", err)
	}
}
