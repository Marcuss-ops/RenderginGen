// canonical_profile.go owns the POLICY of the canonical output lane: which
// render shapes require the canonical post-pass, whether audio is kept, and
// which encoder profile (if any) the renderer may select. It contains no
// ffmpeg invocation and no receipt I/O, so a policy change cannot silently
// alter the emitted avcC or the verification path.
package processor

import (
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// isCanonicalizedOutputProfile reports whether a plan requests an output lane
// whose bytes must be re-encoded to the canonical x264 avcC before finalize.
// V3 (video-only overlays) and V1 (assembly-ready clips) share the exact same
// canonical video recipe. V2 pins a different H.264 level and is deliberately
// excluded until it has its own certified recipe.
func isCanonicalizedOutputProfile(profileID string) bool {
	return profileID == media.ProfileVeloxOverlayVideoOnlyV3 ||
		profileID == media.ProfileVeloxAssemblyReadyV1
}

// isCanonicalOverlayV3 identifies the video-only lane, which canonicalizes
// without an audio stream.
func isCanonicalOverlayV3(profileID string) bool {
	return profileID == media.ProfileVeloxOverlayVideoOnlyV3
}

// canonicalEncoderProfile is the wire name of the single-session canonical
// x264 lane (Chronon output_spec.encoder_profile).
const canonicalEncoderProfile = "canonical_x264"

// canonicalSingleSessionEnabled selects the canonical x264 lane automatically
// for eligible renders. It is false because the 2026-11-16 A/B measured the
// lane at 39.86 s against 4.88 s of NVENC plus 0.93 s of canonical post-pass on
// the same 49-frame 1080p24 plan (quiet host, software compositor): the lane
// pays ~635 ms of backend overhead per frame, so selecting it by default is a
// ~6.9x wall-time regression. Gate §8 accepts the lane only when the total does
// not worsen; it worsens. The lane remains reachable through the explicit
// output_spec.encoder_profile=canonical_x264 (CLI: --encoder-profile) for
// copy-eligibility experiments, and this switch is the single place to
// re-enable it once the per-frame overhead is removed and the A/B passes.
const canonicalSingleSessionEnabled = false

// canonicalEncoderProfileEligible reports whether a render has the exact shape
// the canonical profile certifies: the V3 lane at 1920x1080, 24/1, video-only.
// Requesting the lane elsewhere makes Chronon fail closed (it verifies both the
// geometry and the avcC bytes), so this is a contract rule, not an optimization
// detail. V1 (assembly-ready, carries audio) and V2 (different level) keep the
// post-render pass.
func canonicalEncoderProfileEligible(metadata planMetadata, hasAudio bool) bool {
	if metadata.ProfileID != media.ProfileVeloxOverlayVideoOnlyV3 || hasAudio {
		return false
	}
	return metadata.Width == 1920 && metadata.Height == 1080 &&
		metadata.FPSNum == 24 && metadata.FPSDen == 1
}

// canonicalEncoderProfileFor returns the encoder profile a render must request.
// While canonicalSingleSessionEnabled is false it always returns empty, so the
// renderer emits its default stream and the worker keeps the canonical
// post-pass; see the switch comment for the measured reason.
func canonicalEncoderProfileFor(metadata planMetadata, hasAudio bool) string {
	if !canonicalSingleSessionEnabled || !canonicalEncoderProfileEligible(metadata, hasAudio) {
		return ""
	}
	return canonicalEncoderProfile
}

// CanonicalProfilePolicy is the resolved canonical lane decision. It is a
// value so callers cannot re-derive eligibility from a different subset of the
// plan; Validate rejects an internally inconsistent policy before any encode.
type CanonicalProfilePolicy struct {
	Eligible  bool
	ProfileID string
	KeepAudio bool
	FPSNum    int
	FPSDen    int
	Width     int
	Height    int
}

// ResolveCanonicalProfile is the single policy entry point. Eligible means the
// artifact must be certified by the canonical x264 recipe: V3 (video-only) or
// V1 (assembly-ready, audio copied). The output geometry and cadence are always
// the profile's certified 1920x1080@24, never the source's.
func ResolveCanonicalProfile(metadata planMetadata, hasAudio bool) CanonicalProfilePolicy {
	policy := CanonicalProfilePolicy{
		FPSNum: 24, FPSDen: 1, Width: 1920, Height: 1080,
	}
	if !isCanonicalizedOutputProfile(metadata.ProfileID) {
		return policy
	}
	policy.Eligible = true
	policy.ProfileID = metadata.ProfileID
	// The recipe, not an observed stream, decides audio handling: V1 copies the
	// optional audio map ("0:a:0?") even when the source has none, while V3
	// emits no audio at all. hasAudio only affects the single-session encoder
	// eligibility above, not the post-pass policy.
	policy.KeepAudio = !isCanonicalOverlayV3(metadata.ProfileID)
	return policy
}

// Validate fails closed on a policy that cannot be executed as certified.
func (p CanonicalProfilePolicy) Validate() error {
	if !p.Eligible {
		return nil
	}
	if !isCanonicalizedOutputProfile(p.ProfileID) {
		return fmt.Errorf("canonical policy: active profile %q is not canonicalized", p.ProfileID)
	}
	if p.Width != 1920 || p.Height != 1080 || p.FPSNum != 24 || p.FPSDen != 1 {
		return fmt.Errorf("canonical policy: geometry %dx%d@%d/%d is not the certified 1920x1080@24/1",
			p.Width, p.Height, p.FPSNum, p.FPSDen)
	}
	if p.KeepAudio != !isCanonicalOverlayV3(p.ProfileID) {
		return fmt.Errorf("canonical policy: keep_audio=%t contradicts profile %q", p.KeepAudio, p.ProfileID)
	}
	return nil
}
