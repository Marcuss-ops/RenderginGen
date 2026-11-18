package processor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// TestResolveCanonicalProfileMatrix pins the policy boundary: only V3 and V1
// require the canonical post-pass, V3 never keeps audio, and the certified
// output shape is always 1920x1080@24/1. Geometry is intentionally not an
// eligibility input for the post-pass — the recipe normalizes it.
func TestResolveCanonicalProfileMatrix(t *testing.T) {
	base := planMetadata{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1}
	cases := []struct {
		name     string
		profile  string
		audio    bool
		eligible bool
		want     CanonicalProfilePolicy
	}{
		{
			name:     "V3 video only is canonicalized without audio",
			profile:  media.ProfileVeloxOverlayVideoOnlyV3,
			eligible: true,
			want: CanonicalProfilePolicy{
				Eligible: true, ProfileID: media.ProfileVeloxOverlayVideoOnlyV3,
				KeepAudio: false, FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
			},
		},
		{
			name:     "V3 with audio still takes the post-pass and drops audio",
			profile:  media.ProfileVeloxOverlayVideoOnlyV3,
			audio:    true,
			eligible: true,
			want: CanonicalProfilePolicy{
				Eligible: true, ProfileID: media.ProfileVeloxOverlayVideoOnlyV3,
				KeepAudio: false, FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
			},
		},
		{
			name:     "V1 assembly-ready is canonicalized and keeps audio",
			profile:  media.ProfileVeloxAssemblyReadyV1,
			audio:    true,
			eligible: true,
			want: CanonicalProfilePolicy{
				Eligible: true, ProfileID: media.ProfileVeloxAssemblyReadyV1,
				KeepAudio: true, FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
			},
		},
		{
			name:     "V2 keeps its own bytes",
			profile:  media.ProfileVeloxOverlayVideoOnlyV2,
			eligible: false,
			want: CanonicalProfilePolicy{
				FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
			},
		},
		{
			name:     "unset profile keeps its own bytes",
			eligible: false,
			want: CanonicalProfilePolicy{
				FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := base
			metadata.ProfileID = tc.profile
			got := ResolveCanonicalProfile(metadata, tc.audio)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ResolveCanonicalProfile = %+v, want %+v", got, tc.want)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("policy Validate: %v", err)
			}
			if got.Eligible != tc.eligible {
				t.Fatalf("Eligible = %t, want %t", got.Eligible, tc.eligible)
			}
		})
	}
}

func TestCanonicalProfilePolicyValidateRejectsContradictions(t *testing.T) {
	cases := []struct {
		name   string
		policy CanonicalProfilePolicy
	}{
		{
			name: "unknown active profile",
			policy: CanonicalProfilePolicy{
				Eligible: true, ProfileID: "unknown", FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
			},
		},
		{
			name: "wrong geometry",
			policy: CanonicalProfilePolicy{
				Eligible: true, ProfileID: media.ProfileVeloxOverlayVideoOnlyV3,
				FPSNum: 24, FPSDen: 1, Width: 1280, Height: 720,
			},
		},
		{
			name: "V3 keeping audio",
			policy: CanonicalProfilePolicy{
				Eligible: true, ProfileID: media.ProfileVeloxOverlayVideoOnlyV3, KeepAudio: true,
				FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
			},
		},
		{
			name: "V1 dropping audio",
			policy: CanonicalProfilePolicy{
				Eligible: true, ProfileID: media.ProfileVeloxAssemblyReadyV1,
				FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.policy.Validate(); err == nil {
				t.Fatalf("policy %+v passed validation", tc.policy)
			}
		})
	}
}

// TestCanonicalOutputFFmpegArgsGolden pins the exact x264 recipe. Every flag
// below is part of the emitted avcC identity (refs/B-frames/keyint/VUI), so a
// changed argument is a re-certification event, not a cosmetic edit.
func TestCanonicalOutputFFmpegArgsGolden(t *testing.T) {
	head := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", "in.mp4",
		"-map", "0:v:0",
	}
	tail := []string{
		"-fps_mode", "cfr",
		"-r", "24/1",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-refs", "1",
		"-profile:v", "high",
		"-level:v", "4.0",
		"-pix_fmt", "yuv420p",
		"-g", "48",
		"-bf", "0",
		"-sc_threshold", "0",
		"-x264-params", "scenecut=0:open-gop=0:keyint=48:min-keyint=48",
		"-colorspace", "bt709",
		"-color_primaries", "bt709",
		"-color_trc", "bt709",
		"-color_range", "tv",
		"-video_track_timescale", "90000",
		"-movflags", "+faststart",
		"out.mp4",
	}

	videoOnly := append(append(append([]string{}, head...), "-an", "-sn", "-dn"), tail...)
	if got := canonicalOutputFFmpegArgs("in.mp4", "out.mp4", false, 24, 1); !reflect.DeepEqual(got, videoOnly) {
		t.Fatalf("video-only recipe drifted:\n got %s\nwant %s", strings.Join(got, " "), strings.Join(videoOnly, " "))
	}

	withAudio := append(append(append([]string{}, head...), "-map", "0:a:0?", "-c:a", "copy"), tail...)
	if got := canonicalOutputFFmpegArgs("in.mp4", "out.mp4", true, 24, 1); !reflect.DeepEqual(got, withAudio) {
		t.Fatalf("audio recipe drifted:\n got %s\nwant %s", strings.Join(got, " "), strings.Join(withAudio, " "))
	}
}
