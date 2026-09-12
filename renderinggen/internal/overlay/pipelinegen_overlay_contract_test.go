package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/schemaval"
)

// pipelineGenOverlayPlan is the VERBATIM bytes emitted by PipelineGen's
// MapClipPlanToOverlayPlan (internal/platform/renderinggen/clip_plan_mapper.go)
// for a sealed clip plan carrying a resolved entity overlay. Only the source
// digest — which PipelineGen derives from a temp file in its own test — is
// normalized to a fixed placeholder; every overlay field (item id, kind,
// template_id, window, asset_refs, url) is the producer's real output.
//
// This is the cross-repo half of the single-pass overlay contract: it proves
// the producer's actual payload is accepted by THIS compiler and lowers to a
// timed video layer, so the clip is encoded once. Deletion of the legacy
// post-render compositor is gated on this plus the GPU artifact comparison.
const pipelineGenOverlayPlan = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"overlay-clip-1","video_id":"source-asset-001","width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":4000,"output_profile_id":"VELOX_ASSEMBLY_READY_V1","source":{"asset_id":"source-asset-001","path":"assets/semantic/source-asset-001/source.mp4","sha256":"f0fb655910f46b3cfafd957111dec86106f98923c17d848454ae4a8667611178"},"audio":{"mode":"copy_if_compatible","codec":"aac","sample_rate":48000,"channels":2},"items":[{"id":"overlay-eeeeeeeeeeeeeeee","kind":"video_overlay","template_id":"VIDEO_OVERLAY","motion_params":{},"text":"","start_ms":1000,"end_ms":3000,"params":{"fit":"cover"},"asset_refs":[{"asset_id":"overlay-eeeeeeeeeeeeeeee","sha256":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","url":"assets/semantic/overlay-eeeeeeeeeeeeeeee/overlay.mp4","media_type":"video/mp4"}]}]}`

// TestPipelineGenOverlayPlanLowersToTimedVideoLayer is the producer→worker
// boundary test for the single-pass overlay path. It feeds PipelineGen's real
// mapper output through the one compiler and pins:
//
//   - the template resolves (no silent unknown-template fall-through);
//   - it lowers to a video layer (not a text/image layer);
//   - the layer's window is exactly the declared [start_ms, end_ms) at 24fps;
//   - the segment is registered as a content-addressed manifest asset;
//   - the layer composites over the source (appended last).
//
// Together with the Chronon VideoNode's (frame - layer_start) media sampling
// this is what makes the clip encode ONCE instead of twice.
func TestPipelineGenOverlayPlanLowersToTimedVideoLayer(t *testing.T) {
	result, err := CompileSemantic([]byte(pipelineGenOverlayPlan))
	if err != nil {
		t.Fatalf("PipelineGen overlay plan must compile: %v", err)
	}
	if len(result.UnknownTemplates) != 0 {
		t.Fatalf("VIDEO_OVERLAY must be a registered template, got unknown: %v", result.UnknownTemplates)
	}

	var overlays []Layer
	for _, layer := range result.Plan.Layers {
		if layer.Type == "video" && layer.ID != "source" {
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
	if !strings.Contains(overlay.Source, "overlay-eeeeeeeeeeeeeeee") {
		t.Fatalf("overlay source %q does not reference the declared segment asset", overlay.Source)
	}
	if !strings.HasSuffix(overlay.Source, "overlay.mp4") {
		t.Fatalf("overlay source %q must resolve the segment's .mp4 logical path", overlay.Source)
	}
	// The overlay composites over the clip: it must be the top-most layer.
	if result.Plan.Layers[len(result.Plan.Layers)-1].ID != overlay.ID {
		t.Fatalf("overlay layer must be top-most, got %v", layerIDs(result.Plan.Layers))
	}

	// The segment must ship as a content-addressed manifest asset so the
	// worker can materialize the exact bytes the plan references.
	segmentSHA := strings.Repeat("e", 64)
	found := false
	for _, asset := range result.Assets {
		if asset.Hash == segmentSHA {
			found = true
			if !strings.HasSuffix(asset.LogicalPath, "overlay.mp4") {
				t.Fatalf("staged segment logical path = %q, want an overlay.mp4 path", asset.LogicalPath)
			}
		}
	}
	if !found {
		t.Fatalf("overlay segment %s missing from the asset manifest: %+v", segmentSHA[:12], result.Assets)
	}
}

// TestPipelineGenOverlayPlanIsContractValid validates the producer's real
// payload against the published overlay-plan.v1 schema, so a producer field
// rename can never be accepted here and rejected by a schema-validating
// deployment.
func TestPipelineGenOverlayPlanIsContractValid(t *testing.T) {
	overlaySchema := contractPresetPath(t)
	if err := schemaval.ValidateFile([]byte(pipelineGenOverlayPlan), overlaySchema); err != nil {
		t.Fatalf("PipelineGen overlay plan is not valid overlay-plan.v1: %v", err)
	}
}
