package overlay

import (
	"fmt"
	"strings"
	"testing"
)

// An image_chain whose chain_camera names a camera-pan direction must lower as
// ONE group choreography: every segment stays on the strip for the whole chain,
// the strip travels so each segment reaches the centre at its own start, and no
// segment keeps its single-image entrance or the fly-under handoff.
func TestCompileImageChainCameraPanLowersOneStrip(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-pan","video_id":"v",
		"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":9000,
		"items":[{
			"id":"chain-pan-1","kind":"image_chain","template_id":"IMAGE_CHAIN",
			"start_ms":0,"end_ms":9000,
			"asset_refs":[%s,%s,%s],
			"params":{
				"chain_camera":"image_group_camera_pan_right","chain_count":3,
				"chain_segments":[
					%s,
					%s,
					%s
				]
			}
		}]
	}`,
		panTestAsset("ada"), panTestAsset("grace"), panTestAsset("alan"),
		panTestSegment("ada", "Ada", 0),
		panTestSegment("grace", "Grace", 3_000_000),
		panTestSegment("alan", "Alan", 6_000_000),
	))
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic chain pan: %v", err)
	}
	layers := result.Plan.Layers
	if len(layers) != 6 {
		t.Fatalf("want 3 images + 3 captions, got %d layers: %+v", len(layers), layers)
	}
	for index, id := range []string{"ada", "grace", "alan"} {
		image := findChainPanLayer(t, layers, "seg"+fmt.Sprint(index), "image")
		caption := findChainPanLayer(t, layers, "seg"+fmt.Sprint(index), "text")
		wantOffset := float64(index) * 1280
		if got := image.Position[0]; got != wantOffset {
			t.Fatalf("segment %s image x = %v, want strip slot %v", id, got, wantOffset)
		}
		// Captions stay pinned to the resting canvas slot; only the images
		// travel on the strip, so no moving text surface exists.
		if got := caption.Position[0]; got != 640 {
			t.Fatalf("segment %s caption x = %v, want the resting centre 640", id, got)
		}
		if caption.Animation == nil {
			t.Fatalf("segment %s caption has no pre-start opacity gate", id)
		}
		if move := animationTrack(caption.Animation, "position_x"); move != nil {
			t.Fatalf("segment %s caption moves horizontally: %+v", id, move)
		}
		gate := animationTrack(caption.Animation, "opacity")
		if gate == nil || len(gate.Keyframes) < 3 || gate.Keyframes[0].Value != 0.0 || gate.Keyframes[1].Value != 1.0 {
			t.Fatalf("segment %s caption opacity gate = %+v, want 0 at frame 0 then 1", id, gate)
		}
		if len(caption.ChainPanOffset) != 0 {
			t.Fatalf("segment %s caption ChainPanOffset = %v, want none", id, caption.ChainPanOffset)
		}
		if image.StartFrame > caption.StartFrame || image.StartFrame+image.DurationFrames < caption.StartFrame+caption.DurationFrames {
			t.Fatalf("segment %s image window [%d,%d) does not cover its caption [%d,%d)", id, image.StartFrame, image.StartFrame+image.DurationFrames, caption.StartFrame, caption.StartFrame+caption.DurationFrames)
		}
		if index == 0 && image.StartFrame != 0 {
			t.Fatalf("the first strip member must exist from the chain start, got %d", image.StartFrame)
		}
		if index == 2 && image.StartFrame+image.DurationFrames != 270 {
			t.Fatalf("the last strip member must hold to the chain end, got %d", image.StartFrame+image.DurationFrames)
		}
		wantCaptionStart := int64(index) * 90
		// Overlapping members keep the image on screen for the full segment,
		// so the caption also spans it (minus the two-frame retire margin).
		wantCaptionDuration := int64(88)
		if caption.StartFrame != wantCaptionStart || caption.DurationFrames != wantCaptionDuration {
			t.Fatalf("segment %s caption window = [%d,%d), want [%d,%d)", id, caption.StartFrame, caption.StartFrame+caption.DurationFrames, wantCaptionStart, wantCaptionStart+wantCaptionDuration)
		}
		if image.TransitionIn != nil || caption.TransitionIn != nil {
			t.Fatalf("segment %s still carries a per-segment fly-under transition", id)
		}
		if len(image.ChainPanOffset) != 2 || image.ChainPanOffset[0] != wantOffset {
			t.Fatalf("segment %s ChainPanOffset = %v, want x=%v", id, image.ChainPanOffset, wantOffset)
		}
		if image.Enable3D {
			t.Fatalf("segment %s remained a 3D card; a strip pan is flat canvas motion", id)
		}
		pan := animationTrack(image.Animation, "position_x")
		if pan == nil {
			t.Fatalf("segment %s has no position_x track", id)
		}
		last := pan.Keyframes[len(pan.Keyframes)-1]
		if last.Value != float64(-2560) {
			t.Fatalf("segment %s position_x ends at %v, want -2560 (two canvas steps left)", id, last.Value)
		}
	}
}

func panTestAsset(id string) string {
	return fmt.Sprintf(`{"asset_id":"asset-%s","sha256":"%s","url":"https://example.test/%s.jpg","media_type":"image/jpeg"}`,
		id, strings.Repeat("a", 64), id)
}

func panTestSegment(id, caption string, startUS int64) string {
	return fmt.Sprintf(`{
		"kind":"entity_image","template_id":"image_popup","preset_id":"image_scale_in",
		"image_preset_id":"image_scale_in","motion_id":"image_scale_reveal",
		"entity_id":"ent_%s","entity_caption":"%s","caption_layout":"below",
		"caption_motion_id":"entity_caption_rise",
		"start_us":%d,"duration_us":3000000,
		"asset_id":"asset-%s",
		"assets":[{"asset_id":"asset-%s","sha256":"%s","url":"https://example.test/%s.jpg","media_type":"image/jpeg"}],
		"params":{"position_x":0,"position_y":0,"width":520,"height":520}
	}`, id, caption, startUS, id, id, strings.Repeat("a", 64), id)
}

func findChainPanLayer(t *testing.T, layers []Layer, segment, layerType string) *Layer {
	t.Helper()
	for i := range layers {
		if strings.Contains(layers[i].ID, segment) && layers[i].Type == layerType {
			return &layers[i]
		}
	}
	t.Fatalf("no %s layer for %s in %+v", layerType, segment, layers)
	return nil
}

func animationTrack(animation *LayerAnimation, property string) *AnimationTrack {
	if animation == nil {
		return nil
	}
	for i := range animation.Tracks {
		if animation.Tracks[i].Property == property {
			return &animation.Tracks[i]
		}
	}
	return nil
}
