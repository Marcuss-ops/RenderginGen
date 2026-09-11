package processor

import (
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// TestAudioModeCopyOnlyVocabulary pins which audio modes the worker can
// actually execute. The native A/V mux copies the source stream; anything that
// promises a transcode is a request the worker cannot fulfil.
func TestAudioModeCopyOnlyVocabulary(t *testing.T) {
	honoured := []string{"", "copy", "COPY", "copy_if_compatible", " passthrough ", "mux"}
	for _, mode := range honoured {
		if !audioModeCopyOnly(mode) {
			t.Errorf("audioModeCopyOnly(%q) = false, want true (the mux can honour it)", mode)
		}
	}
	degraded := []string{"transcode", "aac", "encode", "copy_and_transcode"}
	for _, mode := range degraded {
		if audioModeCopyOnly(mode) {
			t.Errorf("audioModeCopyOnly(%q) = true, want false (the worker cannot transcode)", mode)
		}
	}
}

// TestAudioPolicyDegradationIsReported pins the observability fix: a plan
// whose ONLY audio directive is an unsupported mode used to compile with no
// warning at all, so the worker silently copied the source stream where the
// caller asked for a transcode. The warning flag now covers the mode, and a
// copy-family mode with only inert transcode parameters is still surfaced.
func TestAudioPolicyDegradationIsReported(t *testing.T) {
	rawPlan := []byte(`{"source":{"asset_id":"src","path":"assets/semantic/src.mp4"}}`)

	cases := []struct {
		name     string
		audio    *overlay.Audio
		wantWarn bool
	}{
		{name: "no audio policy", audio: nil, wantWarn: false},
		{name: "mode only copy", audio: &overlay.Audio{Mode: "copy"}, wantWarn: false},
		{name: "mode only transcode", audio: &overlay.Audio{Mode: "transcode"}, wantWarn: true},
		{name: "copy mode with inert transcode params", audio: &overlay.Audio{Mode: "copy", Codec: "aac", SampleRate: 44100, Channels: 1}, wantWarn: true},
		{name: "no mode but inert params", audio: &overlay.Audio{Codec: "aac"}, wantWarn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := &overlay.Plan{Output: overlay.Output{Audio: tc.audio}}
			path, warn := audioSourcePathFromPlan(plan, rawPlan, "/ws")
			if warn != tc.wantWarn {
				t.Fatalf("warn = %v, want %v", warn, tc.wantWarn)
			}
			if tc.audio == nil || tc.audio.Mode == "" {
				if path != "" {
					t.Fatalf("path = %q, want empty without an audio mode", path)
				}
				return
			}
			if path != "/ws/assets/semantic/src.mp4" {
				t.Fatalf("path = %q, want the materialized semantic source", path)
			}
		})
	}
}
