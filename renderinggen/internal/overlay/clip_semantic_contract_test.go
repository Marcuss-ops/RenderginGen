package overlay

import (
	"encoding/json"
	"math"
	"testing"
)

// sha256 is a valid 64-char hex SHA-256 used throughout clip semantic fixtures.
const clipTestSHA = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
const bgTestSHA = "1122334455667788990011223344556677889900112233445566778899001122"
const subTestSHA = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
const wmTestSHA = "0011223344556677889900112233445566778899001122334455667788990011"

// clipSemanticFixture returns a renderinggen.overlay-plan.v1 JSON fixture
// representing a complete clip render job:
//   - source celebrity clip
//   - background video
//   - foreground at 80% scale (centered)
//   - Montserrat subtitles (burn mode)
//   - text watermark centered
//   - audio copy_if_compatible
//   - items: [] (no entity overlays)
func clipSemanticFixture() []byte {
	return []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "cert-clip-001",
		"video_id":       "src-celebrity-001",
		"width":  1920,
		"height": 1080,
		"fps_num": 30,
		"fps_den": 1,
		"duration_ms": 10000,
		"source": {
			"asset_id": "src-celebrity-001",
			"sha256": "` + clipTestSHA + `"
		},
		"foreground_scale_percent": 80,
		"background": {
			"kind": "video",
			"asset_refs": [{"asset_id": "bg-001", "sha256": "` + bgTestSHA + `"}],
			"loop": true
		},
		"subtitles": {
			"mode": "burn",
			"style_id": "montserrat_default",
			"asset_refs": [{"asset_id": "` + subTestSHA + `", "sha256": "` + subTestSHA + `"}]
		},
		"watermark": {
			"text": "VeloxEditing",
			"position": "center",
			"opacity": 0.8
		},
		"audio": {
			"mode": "copy_if_compatible",
			"codec": "aac",
			"sample_rate": 48000,
			"channels": 2
		},
		"items": []
	}`)
}

// TestClipSemanticContractFull is the killer test for the complete clip render
// bridge: ClipRenderPlanV1 → renderinggen.overlay-plan.v1 → chronon.render-plan.v2.
// It verifies every required property of the compiled Chronon plan.
func TestClipSemanticContractFull(t *testing.T) {
	raw := clipSemanticFixture()

	compiled, assets, semantic, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatalf("semantic input FAIL: CompileIfSemantic returned error: %v", err)
	}
	if !semantic {
		t.Fatal("semantic input FAIL: plan was not recognised as semantic (schema_version missing or wrong)")
	}
	t.Log("semantic input                 PASS")

	// Decode compiled Chronon plan.
	var plan concretePlan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatalf("Chronon schema v2 FAIL: cannot decode compiled plan: %v", err)
	}
	if plan.Schema != "chronon.render-plan.v2" {
		t.Errorf("Chronon schema v2 FAIL: schema = %q, want chronon.render-plan.v2", plan.Schema)
	}
	if plan.Version != 2 {
		t.Errorf("Chronon schema v2 FAIL: version = %d, want 2", plan.Version)
	}
	t.Log("Chronon schema v2              PASS")

	// Verify layers by ID.
	layerByID := map[string]concreteLayer{}
	for _, l := range plan.Layers {
		layerByID[l.ID] = l
	}

	// --- source layer ---
	src, ok := layerByID["source"]
	if !ok {
		t.Error("source layer FAIL: no layer with id=source")
	} else {
		t.Log("source layer                   PASS")
	}

	// --- source duration > 0 ---
	if ok && src.DurationFrames <= 0 {
		t.Errorf("source duration > 0 FAIL: source.duration_frames = %d", src.DurationFrames)
	} else if ok {
		t.Logf("source duration > 0            PASS (%d frames)", src.DurationFrames)
	}

	// --- background layer ---
	bg, bgOK := layerByID["background"]
	if !bgOK {
		t.Error("background duration > 0 FAIL: no layer with id=background")
	} else if bg.DurationFrames <= 0 {
		t.Errorf("background duration > 0 FAIL: background.duration_frames = %d", bg.DurationFrames)
	} else {
		t.Logf("background duration > 0        PASS (%d frames)", bg.DurationFrames)
	}

	// --- source geometry 1536×864 at 80% of 1920×1080 ---
	expectedW := int(math.Round(1920 * 80.0 / 100))
	expectedH := int(math.Round(1080 * 80.0 / 100))
	if ok {
		if len(src.Size) != 2 || int(src.Size[0]) != expectedW || int(src.Size[1]) != expectedH {
			t.Errorf("source geometry %dx%d FAIL: size = %v", expectedW, expectedH, src.Size)
		} else {
			t.Logf("source geometry %dx%d      PASS", expectedW, expectedH)
		}
		// --- source centered 192,108 ---
		expectedX := float64(1920-expectedW) / 2
		expectedY := float64(1080-expectedH) / 2
		if len(src.Position) != 2 || src.Position[0] != expectedX || src.Position[1] != expectedY {
			t.Errorf("source centered %.0f,%.0f FAIL: position = %v", expectedX, expectedY, src.Position)
		} else {
			t.Logf("source centered %.0f,%.0f       PASS", expectedX, expectedY)
		}
	}

	// --- subtitle representation ---
	if _, ok := layerByID["subtitles"]; !ok {
		t.Error("subtitle representation FAIL: no layer with id=subtitles")
	} else {
		t.Log("subtitle representation        PASS")
	}

	// --- watermark representation ---
	if _, ok := layerByID["watermark"]; !ok {
		t.Error("watermark representation FAIL: no layer with id=watermark")
	} else {
		t.Log("watermark representation       PASS")
	}

	// --- audio representation ---
	if plan.Output.Audio == nil {
		t.Error("audio representation FAIL: output.audio is nil")
	} else if plan.Output.Audio.Mode != "copy_if_compatible" {
		t.Errorf("audio representation FAIL: mode = %q, want copy_if_compatible", plan.Output.Audio.Mode)
	} else {
		t.Log("audio representation           PASS")
	}

	// --- compiled assets complete ---
	assetHashes := map[string]bool{}
	for _, a := range assets {
		assetHashes[a.Hash] = true
	}
	wantHashes := []string{
		clipTestSHA,
		bgTestSHA,
		subTestSHA,
	}
	for _, h := range wantHashes {
		if !assetHashes[h] {
			t.Errorf("compiled assets complete FAIL: hash %s not in asset list", h[:16]+"...")
		}
	}
	if !t.Failed() {
		t.Logf("compiled assets complete       PASS (%d assets)", len(assets))
	}
}

// TestClipSemanticItemsEmptyAccepted verifies that a clip with items:[] and
// a valid source/background is accepted (guard on renderable primitives, not items count).
func TestClipSemanticItemsEmptyAccepted(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "clip-no-items",
		"video_id": "src-001",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"duration_ms": 5000,
		"source": {"asset_id": "src-001", "sha256": "` + clipTestSHA + `"},
		"items": []
	}`)
	_, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatalf("items:[] with source FAIL: should be accepted, got: %v", err)
	}
	t.Log("items:[] with source           PASS")
}

// TestClipSemanticNoPrimitiveRejected verifies that a plan with no source,
// no background and no items is rejected.
func TestClipSemanticNoPrimitiveRejected(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "empty",
		"video_id": "v",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"items": []
	}`)
	_, _, _, err := CompileIfSemantic(raw)
	if err == nil {
		t.Fatal("no-primitive plan FAIL: expected rejection, got nil error")
	}
	t.Logf("no-primitive plan rejected     PASS (%v)", err)
}

// TestClipSemanticDurationFromMS verifies that duration_ms seeds the canvas
// duration when items is empty.
func TestClipSemanticDurationFromMS(t *testing.T) {
	// 10000ms at 30fps = 300 frames
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "dur-test",
		"video_id": "src",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"duration_ms": 10000,
		"source": {"asset_id": "src", "sha256": "` + clipTestSHA + `"},
		"items": []
	}`)
	compiled, _, semantic, err := CompileIfSemantic(raw)
	if err != nil || !semantic {
		t.Fatalf("duration_ms FAIL: %v", err)
	}
	var plan concretePlan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	// msFrames(0, 10000, 30, 1) = ceil(10000*30/1000) = 300
	if plan.Canvas.DurationFrames != 300 {
		t.Errorf("duration_ms FAIL: canvas.duration_frames = %d, want 300", plan.Canvas.DurationFrames)
	} else {
		t.Logf("duration_ms → canvas           PASS (%d frames)", plan.Canvas.DurationFrames)
	}
}

// TestClipSemanticForegroundScale verifies centered scaling geometry.
func TestClipSemanticForegroundScale(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "scale-test",
		"video_id": "src",
		"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
		"duration_ms": 3000,
		"source": {"asset_id": "src", "sha256": "` + clipTestSHA + `"},
		"foreground_scale_percent": 80,
		"items": []
	}`)
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatalf("foreground_scale FAIL: %v", err)
	}
	var plan concretePlan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	for _, l := range plan.Layers {
		if l.ID != "source" {
			continue
		}
		wantW := int(math.Round(1920 * 0.8))
		wantH := int(math.Round(1080 * 0.8))
		if len(l.Size) != 2 || int(l.Size[0]) != wantW || int(l.Size[1]) != wantH {
			t.Errorf("foreground_scale geometry FAIL: size=%v, want [%d %d]", l.Size, wantW, wantH)
		}
		wantX := float64(1920-wantW) / 2
		wantY := float64(1080-wantH) / 2
		if len(l.Position) != 2 || l.Position[0] != wantX || l.Position[1] != wantY {
			t.Errorf("foreground_scale centered FAIL: position=%v, want [%g %g]", l.Position, wantX, wantY)
		}
		t.Logf("foreground_scale geometry+centered PASS (size=%v, pos=%v)", l.Size, l.Position)
		return
	}
	t.Error("foreground_scale FAIL: no source layer found")
}

// TestClipSemanticAudioLowering verifies audio policy is present in the output block.
func TestClipSemanticAudioLowering(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "audio-test",
		"video_id": "src",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"duration_ms": 5000,
		"source": {"asset_id": "src", "sha256": "` + clipTestSHA + `"},
		"audio": {"mode": "transcode", "codec": "aac", "sample_rate": 44100, "channels": 1},
		"items": []
	}`)
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatalf("audio lowering FAIL: %v", err)
	}
	var plan concretePlan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Output.Audio == nil {
		t.Fatal("audio lowering FAIL: output.audio is nil")
	}
	if plan.Output.Audio.Mode != "transcode" || plan.Output.Audio.SampleRate != 44100 {
		t.Errorf("audio lowering FAIL: %+v", plan.Output.Audio)
	}
	t.Log("audio lowering                 PASS")
}
