package processor

import (
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// canonicalEncoderProfileEligibility is the contract rule: only the V3 lane at
// 1920x1080@24 video-only has the exact shape the profile certifies. Requesting
// it elsewhere makes Chronon fail closed (geometry and avcC are verified), so
// this rule is part of the contract, not an optimization detail.
func TestCanonicalEncoderProfileEligibility(t *testing.T) {
	v3 := planMetadata{
		ProfileID: media.ProfileVeloxOverlayVideoOnlyV3,
		Width:     1920, Height: 1080, FPSNum: 24, FPSDen: 1,
	}
	cases := []struct {
		name     string
		metadata planMetadata
		audio    bool
		want     bool
	}{
		{"V3 1080p24 video-only", v3, false, true},
		{"V3 with audio keeps the post pass", v3, true, false},
		{"V1 keeps the post pass", planMetadata{
			ProfileID: media.ProfileVeloxAssemblyReadyV1,
			Width:     1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		}, false, false},
		{"V2 keeps the post pass", planMetadata{
			ProfileID: media.ProfileVeloxOverlayVideoOnlyV2,
			Width:     1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		}, false, false},
		{"wrong frame rate stays generic", planMetadata{
			ProfileID: media.ProfileVeloxOverlayVideoOnlyV3,
			Width:     1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		}, false, false},
		{"wrong geometry stays generic", planMetadata{
			ProfileID: media.ProfileVeloxOverlayVideoOnlyV3,
			Width:     1280, Height: 720, FPSNum: 24, FPSDen: 1,
		}, false, false},
		{"no profile stays generic", planMetadata{
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		}, false, false},
	}
	for _, tc := range cases {
		if got := canonicalEncoderProfileEligible(tc.metadata, tc.audio); got != tc.want {
			t.Errorf("%s: canonicalEncoderProfileEligible = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// TestCanonicalEncoderProfileFor pins the 2026-11-16 A/B decision: even an
// eligible V3 render does NOT select the single-session lane by default. The
// measured total was 39.86 s canonical against 4.88 s NVENC + 0.93 s canonical
// post-pass on the same 49-frame plan (~6.9x regression), so gate §8 rejects
// the lane as the default. The explicit encoder_profile payload still works.
func TestCanonicalEncoderProfileFor(t *testing.T) {
	if canonicalSingleSessionEnabled {
		t.Fatal("the canonical single-session lane was re-enabled without a passing A/B")
	}
	v3 := planMetadata{
		ProfileID: media.ProfileVeloxOverlayVideoOnlyV3,
		Width:     1920, Height: 1080, FPSNum: 24, FPSDen: 1,
	}
	if got := canonicalEncoderProfileFor(v3, false); got != "" {
		t.Fatalf("eligible V3 render selected %q by default; the measured A/B requires opt-in", got)
	}
	if got := canonicalEncoderProfileFor(v3, true); got != "" {
		t.Fatalf("V3 with audio selected %q, want the post pass", got)
	}
}
