package overlay

import (
	"strings"
	"testing"
)

// TestClipSemanticAudioLowering verifies audio policy is accepted while the
// Chronon v2 output remains schema-compatible (audio is handled by the worker).
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
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("audio lowering FAIL: %v", err)
	}
	compiled := result.Plan
	if compiled.Output.Audio == nil {
		t.Fatal("audio lowering FAIL: semantic audio must populate the typed Output.Audio policy")
	}
	if compiled.Output.Audio.Mode != "transcode" || compiled.Output.Audio.Codec != "aac" || compiled.Output.Audio.SampleRate != 44100 || compiled.Output.Audio.Channels != 1 {
		t.Fatalf("audio lowering FAIL: policy = %+v", compiled.Output.Audio)
	}
	// The policy must NOT reach the Chronon v2 JSON document.
	encoded, err := compiled.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "\"audio\"") {
		t.Fatal("audio lowering FAIL: output.audio leaked into the Chronon v2 JSON document")
	}
	t.Log("audio lowering                 PASS (typed policy, json:\"-\")")
}
