// canonical_encode.go owns the RECIPE of the canonical output: the exact
// ffmpeg argument list and the atomic single-artifact rewrite. Policy lives in
// canonical_profile.go and receipt verification in canonical_receipt.go; this
// file must not acquire either decision.
package processor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// canonicalOutputFFmpegArgs is the exact canonical encoder recipe. The avcC
// identity depends on every one of these settings, not just profile/level:
// refs, B-frames, scene-cut/keyint behaviour and the VUI colour tags all land
// in the parameter sets. Do not reorder or "simplify" this list without
// re-certifying CanonicalVideoExtradataSHA256.
//
// keepAudio copies the source audio track untouched. The audio mapping does not
// change the video parameter sets, so the same recipe certifies both the
// video-only V3 overlay and the assembly-ready V1 clip (which keeps its audio).
//
// fpsNum/fpsDen are REQUIRED: the canonical profile is a property of the
// emitted bytes, not of the source. The previous passthrough preserved a 25 fps
// source and the profile gate rejected the artifact after the render (live
// clip.render failure: fps=25/1, want 24/1).
func canonicalOutputFFmpegArgs(input, output string, keepAudio bool, fpsNum, fpsDen int) []string {
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", input,
		"-map", "0:v:0",
	}
	if keepAudio {
		args = append(args, "-map", "0:a:0?", "-c:a", "copy")
	} else {
		args = append(args, "-an", "-sn", "-dn")
	}
	args = append(args,
		// Emit the profile cadence, not the source cadence. CFR duplicates or
		// drops frames exactly as the assembly contract requires; passthrough
		// let a 25 fps source reach the profile gate and fail after the render.
		"-fps_mode", "cfr",
		"-r", fmt.Sprintf("%d/%d", fpsNum, fpsDen),
	)
	return append(args,
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
		output,
	)
}

// canonicalizeOutputMP4 rewrites one artifact to the canonical stream of the
// requested profile. It is idempotent: an artifact that already satisfies the
// profile — including a native artifact whose avcC is in the explicit NVENC
// recertification allowlist — is left untouched, so a retry after
// canonicalization does not re-encode (and re-hash) it.
//
// The rewrite is atomic: ffmpeg writes a sibling temp file, the temp file is
// probed and certified, and only then is it renamed over the artifact. A
// failed encode therefore leaves the original bytes untouched, and no other
// process can observe a half-written MP4.
func canonicalizeOutputMP4(ctx context.Context, path, profileID string, acceptedCodecConfig ...string) (bool, error) {
	profile, err := media.ResolveProfile(profileID)
	if err != nil {
		return false, err
	}
	if len(acceptedCodecConfig) > 0 {
		profile.AcceptedCodecConfigSHA256 = append(append([]string(nil), profile.AcceptedCodecConfigSHA256...), acceptedCodecConfig...)
	}
	// The single-session canonical lane publishes the avcC it emitted in the
	// renderer receipt (TICKET-CANONICAL-X264-SINGLE-SESSION). When that
	// receipt certifies the profile's exact parameter sets for THIS file, the
	// artifact is already the deliverable: the pass has nothing to decide, so
	// it neither re-encodes nor pays the confirmation ffprobe that used to be
	// the last duplicated read in the finalize.
	if receiptCertifiesCanonicalIdentity(path, profile.CodecConfigSHA256) {
		return false, nil
	}
	// A renderer receipt describes bytes the renderer just wrote. With no
	// NVENC allowlist configured those bytes cannot already carry the canonical
	// x264 identity (refs=1/bf=0/SPS-PPS of the x264 lane), so the pass must
	// run and the source probe would only re-read a file Chronon just emitted:
	// two ffprobe processes that can never change the decision. The probe is
	// kept exactly where it can change it — an allowlist may accept a
	// recertified native avcC as-is, and an artifact with no render receipt (a
	// retry of a previously canonicalized file) may already be canonical.
	if needsCanonicalEncodeFromRenderReceipt(path, profile.CodecConfigSHA256, len(profile.AcceptedCodecConfigSHA256) > 0) {
		return encodeCanonicalOutput(ctx, path, profile, profileID)
	}
	existing, err := media.ProbeFile(ctx, path)
	if err != nil {
		return false, fmt.Errorf("probe source: %w", err)
	}
	if err := profile.ValidateProbe(existing); err == nil {
		return false, nil
	}
	return encodeCanonicalOutput(ctx, path, profile, profileID)
}

// encodeCanonicalOutput rewrites one artifact with the exact canonical x264
// recipe, certifies the emitted bytes and installs them atomically.
func encodeCanonicalOutput(ctx context.Context, path string, profile media.OutputProfile, profileID string) (bool, error) {
	keepAudio := !isCanonicalOverlayV3(profileID)
	temp := path + ".canonical.tmp.mp4"
	_ = os.Remove(temp)
	defer os.Remove(temp)
	cmd := exec.CommandContext(ctx, "ffmpeg", canonicalOutputFFmpegArgs(path, temp, keepAudio, profile.FPSNum, profile.FPSDen)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("ffmpeg canonical encode: %w: %s", err, output)
	}

	probeStart := time.Now()
	probe, err := media.ProbeFile(ctx, temp)
	if err != nil {
		return false, fmt.Errorf("probe canonical output: %w", err)
	}
	probeMS := float64(time.Since(probeStart).Microseconds()) / 1000
	if err := profile.ValidateProbe(probe); err != nil {
		return false, fmt.Errorf("canonical output rejected by %s: %w", profile.ID, err)
	}

	hashStart := time.Now()
	digest, size, err := hashio.File(temp)
	if err != nil {
		return false, fmt.Errorf("hash canonical output: %w", err)
	}
	hashMS := float64(time.Since(hashStart).Microseconds()) / 1000
	if err := writeCanonicalOverlayReceipt(path, probe, digest, size, probeMS, hashMS); err != nil {
		return false, err
	}
	if err := os.Rename(temp, path); err != nil {
		return false, fmt.Errorf("publish canonical output: %w", err)
	}
	return true, nil
}
