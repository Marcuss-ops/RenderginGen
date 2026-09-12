package overlay

import (
	"strconv"
	"strings"
	"testing"
)

const videoOverlaySHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// videoOverlayPlanJSON is the minimal clip-render plan carrying a pre-rendered
// overlay segment as a semantic item. It mirrors exactly what PipelineGen's
// clip_plan_mapper emits for the single-pass overlay path.
func videoOverlayPlanJSON(startMS, endMS int64, kind, template, fit string) string {
	params := "{}"
	if fit != "" {
		params = `{"fit":"` + fit + `"}`
	}
	return `{
	  "schema_version": "renderinggen.overlay-plan.v1",
	  "plan_id": "clip-overlay-1",
	  "video_id": "source-asset-001",
	  "width": 1920,
	  "height": 1080,
	  "fps_num": 24,
	  "fps_den": 1,
	  "duration_ms": 4000,
	  "output_profile_id": "VELOX_ASSEMBLY_READY_V1",
	  "style_profile": "discovery",
	  "source": {"asset_id": "source-asset-001", "path": "assets/semantic/source-asset-001/source.mp4", "sha256": "` + videoOverlaySHA + `"},
	  "audio": {"mode": "copy_if_compatible", "codec": "aac", "sample_rate": 48000, "channels": 2},
	  "items": [{
	    "id": "overlay-item-1",
	    "kind": "` + kind + `",
	    "template_id": "` + template + `",
	    "preset_id": "",
	    "motion_id": "",
	    "motion_params": {},
	    "text": "",
	    "start_ms": ` + itoa(startMS) + `,
	    "end_ms": ` + itoa(endMS) + `,
	    "params": ` + params + `,
	    "asset_refs": [{"asset_id": "overlay-segment-1", "sha256": "` + videoOverlaySHA + `", "url": "assets/semantic/overlay-segment-1/overlay.mp4", "media_type": "video/mp4"}]
	  }]
	}`
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// TestCompileVideoOverlayLowersToTimedVideoLayer pins the single-pass
// contract: a video_overlay item becomes a video layer whose Source is the
// content-addressed segment and whose [StartFrame, StartFrame+DurationFrames)
// window is exactly the declared [start_ms, end_ms). Because Chronon's
// VideoNode samples the media at (frame - layer_start), the segment's first
// frame lands on the declared start frame — the clip is encoded ONCE.
func TestCompileVideoOverlayLowersToTimedVideoLayer(t *testing.T) {
	plan, assets, _, unknown, err := compileSemantic([]byte(videoOverlayPlanJSON(1000, 3000, "video_overlay", "VIDEO_OVERLAY", "")))
	if err != nil {
		t.Fatalf("compileSemantic(video overlay): %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("registered video overlay template must not fall through: %v", unknown)
	}
	var overlayLayer *Layer
	for i := range plan.Layers {
		if plan.Layers[i].Type == "video" && plan.Layers[i].ID != "source" {
			overlayLayer = &plan.Layers[i]
		}
	}
	if overlayLayer == nil {
		t.Fatalf("no overlay video layer emitted: %+v", plan.Layers)
	}
	// 24fps → 1000ms = frame 24, 3000ms = frame 72 (exclusive).
	if overlayLayer.StartFrame != 24 || overlayLayer.DurationFrames != 48 {
		t.Fatalf("overlay window = %d+%d, want 24+48 (start_ms/end_ms at 24fps)", overlayLayer.StartFrame, overlayLayer.DurationFrames)
	}
	if overlayLayer.Source == "" {
		t.Fatal("video overlay layer must carry source (video layers do not use asset)")
	}
	if !strings.Contains(overlayLayer.Source, "overlay-segment-1") {
		t.Fatalf("source %q does not reference the overlay segment asset", overlayLayer.Source)
	}
	if overlayLayer.Fit != "cover" {
		t.Fatalf("fit = %q, want the full-canvas default cover", overlayLayer.Fit)
	}
	// The overlay layer must be appended AFTER the source so it composites on top.
	if plan.Layers[len(plan.Layers)-1].ID != overlayLayer.ID {
		t.Fatalf("overlay layer must be last (top-most), got order %+v", layerIDs(plan.Layers))
	}
	// The segment must be staged as a content-addressed manifest asset.
	found := false
	for _, a := range assets {
		if a.Hash == videoOverlaySHA {
			found = true
		}
	}
	if !found {
		t.Fatalf("overlay segment missing from the asset manifest: %+v", assets)
	}
}

// TestCompileVideoOverlayAcceptsKindSynonyms pins the accepted vocabulary.
func TestCompileVideoOverlayAcceptsKindSynonyms(t *testing.T) {
	for _, tc := range []struct{ kind, template string }{
		{"video", "VIDEO_OVERLAY"},
		{"rendered_overlay", "RENDERED_OVERLAY"},
	} {
		plan, _, _, _, err := compileSemantic([]byte(videoOverlayPlanJSON(0, 1000, tc.kind, tc.template, "contain")))
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.kind, tc.template, err)
		}
		var overlay *Layer
		for i := range plan.Layers {
			if plan.Layers[i].ID != "source" && plan.Layers[i].Type == "video" {
				overlay = &plan.Layers[i]
			}
		}
		if overlay == nil || overlay.Fit != "contain" {
			t.Fatalf("%s/%s: overlay layer = %+v, want fit=contain", tc.kind, tc.template, overlay)
		}
	}
}

// TestCompileVideoOverlayFailsClosedWithoutAsset pins the fail-closed rule: a
// video overlay with no asset_refs is a compile error, never a silently
// empty layer.
func TestCompileVideoOverlayFailsClosedWithoutAsset(t *testing.T) {
	raw := strings.Replace(videoOverlayPlanJSON(0, 1000, "video_overlay", "VIDEO_OVERLAY", ""),
		`"asset_refs": [{"asset_id": "overlay-segment-1", "sha256": "`+videoOverlaySHA+`", "url": "assets/semantic/overlay-segment-1/overlay.mp4", "media_type": "video/mp4"}]`,
		`"asset_refs": []`, 1)
	if _, _, _, _, err := compileSemantic([]byte(raw)); err == nil {
		t.Fatal("video overlay without asset_refs must fail closed")
	}
}

// TestCompileVideoOverlayRejectsUnknownFit pins fail-closed geometry: an
// unsupported fit is rejected instead of silently degrading to a default.
func TestCompileVideoOverlayRejectsUnknownFit(t *testing.T) {
	if _, _, _, _, err := compileSemantic([]byte(videoOverlayPlanJSON(0, 1000, "video_overlay", "VIDEO_OVERLAY", "squish"))); err == nil {
		t.Fatal("unsupported fit must fail closed")
	}
}

func layerIDs(layers []Layer) []string {
	out := make([]string, 0, len(layers))
	for _, l := range layers {
		out = append(out, l.ID+":"+l.Type)
	}
	return out
}
