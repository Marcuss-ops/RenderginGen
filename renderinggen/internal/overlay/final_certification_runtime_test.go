//go:build certification

// Final rendering certification — runtime level.
//
// These tests render real MP4s with the real chronon3d_cli binary through the
// same plans the compile-level suite certifies, then run the three validation
// layers on every output:
//
//	A. structural  — ffprobe (resolution, fps, frame count, duration)
//	B. decode      — full bitstream decode, pass only on ffmpeg exit 0
//	C. pixel       — background preservation outside the entity bbox,
//	                 no fully-black frame (the Vulkan black-output regression)
//
// The suite is strictly OPT-IN and skips unless CHRONON_BIN names the engine:
// compile-level certification (final_certification_test.go) always runs, this one
// needs a GPU-capable build environment. It is never enabled by DISCOVERY of a
// build beside this repository — see chrononBinFor for why discovery alone is not
// consent to render video from a bare `go test ./...`.
//
// RENDERINGGEN_SKIP_GPU_E2E remains as the explicit opt-out (any non-empty value
// skips every test that would drive the real engine), which is what
// `make test-unit` and CI set to give a fast, deterministic gate everywhere. See
// TestRuntimeCertificationOptOut.

package overlay

import (
	"testing"
)

// TestFinal_AppleStylesPixelByPixel certifies the three checked-in Apple
// compositions at raster level. The image preset is rendered at an animation
// frame where the profiles are distinguishable, then every pixel is compared;
// this catches a catalog change that still compiles but collapses styles to
// the same visual output.
func TestFinal_AppleStylesPixelByPixel(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()
	profiles := []struct {
		name   string
		preset string
	}{
		{"crime", "image_scale_in"},
		{"discovery", "image_slide_right"},
		{"young", "modern_rounded_pop"},
	}
	frames := make([][]byte, 0, len(profiles))
	for _, profile := range profiles {
		video := renderCertificationPlan(t, bin, assetsRoot, outDir, profile.preset, "apple_style_"+profile.name)
		probeMP4Structural(t, video)
		decodeFully(t, video)
		// The fixture starts at frame 10 and the catalog image enter motion is
		// eight frames long; frame 12 is inside that active transition window.
		frames = append(frames, extractFramePNG(t, video, 12))
	}
	for i := 0; i < len(frames); i++ {
		for j := i + 1; j < len(frames); j++ {
			assertPixelDifference(t, frames[i], frames[j], 1000)
		}
	}
}
