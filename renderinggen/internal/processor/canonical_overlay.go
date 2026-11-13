// canonical_overlay.go owns the final, deterministic encode of the
// canonicalized output profiles: VELOX_OVERLAY_VIDEO_ONLY_V3 (video-only
// overlays) and VELOX_ASSEMBLY_READY_V1 (assembly-ready clips with audio).
//
// A canonicalized profile does not merely declare the canonical stream facts:
// it must EMIT them. The native NVENC lane produces a different avcC than the
// canonical x264 lane (the live clip failure: avcC 111186fe…), and the
// historical overlay lane emitted 1/12288. This pass therefore re-encodes the
// artifact with the exact canonical x264 recipe, verifies the resulting bytes
// with ffprobe against the requested profile, and writes a receipt
// that describes the FINAL bytes (the renderer's original receipt describes the
// pre-canonicalization file and must never be used after this pass).
//
// The pass is scoped to the canonicalized profiles only. V2 keeps its existing
// bytes and cache identity; changing it in place without its own certified
// recipe would invalidate previously certified overlays.
package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// canonicalOverlayReceiptSchema names the receipt producer explicitly. The
// bytes are final after this pass, so a consumer must not read the pre-pass
// Chronon receipt; this schema is the marker that the media facts and hash
// describe the canonicalized artifact.
const canonicalOverlayReceiptSchema = "renderinggen.canonical-overlay-receipt.v1"

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

// canonicalizePreparedOutputs canonicalizes the fused output and every P4
// segment of a prepared job. It serves both canonicalized lanes: the
// video-only V3 overlay and the assembly-ready V1 clip. It returns true when
// at least one artifact was re-encoded; the caller uses that to revoke a
// native certification that no longer describes the final bytes.
func (p *Processor) canonicalizePreparedOutputs(ctx context.Context, prepared *PreparedJob) (bool, error) {
	if prepared == nil || prepared.Plan == nil {
		return false, nil
	}
	metadata := planMetadataOf(prepared.Plan)
	if !isCanonicalizedOutputProfile(metadata.ProfileID) {
		return false, nil
	}
	paths := make([]string, 0, 1+len(prepared.Segments))
	paths = append(paths, prepared.OutputPath)
	for _, segment := range prepared.Segments {
		paths = append(paths, segment.OutputPath)
	}
	return p.canonicalizeOutputPaths(ctx, prepared.Job.ID, paths, prepared.Metrics, metadata.ProfileID)
}

// canonicalizeOutputPaths canonicalizes an explicit set of outputs. The P4
// single-segment retry uses it for the one segment it re-rendered.
func (p *Processor) canonicalizeOutputPaths(ctx context.Context, jobID string, paths []string, metrics map[string]float64, profileID string) (bool, error) {
	seen := make(map[string]struct{}, len(paths))
	changed := false
	var total time.Duration
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		start := time.Now()
		encoded, err := canonicalizeOutputMP4(ctx, path, profileID)
		total += time.Since(start)
		if err != nil {
			return changed, fmt.Errorf("canonicalize %s: %w", filepath.Base(path), err)
		}
		changed = changed || encoded
	}
	if changed {
		if metrics != nil {
			metrics[metricnames.CanonicalizeOverlayUS] = float64(total.Microseconds())
			metrics[metricnames.CanonicalizeOverlayMS] = float64(total.Microseconds()) / 1000
		}
		workerlog.ByJobID(jobID).Infof("canonical output emitted: %d artifact(s) re-encoded to the canonical avcC (profile %s)", len(seen), profileID)
	}
	return changed, nil
}

// canonicalizeOutputMP4 rewrites one artifact to the canonical stream of the
// requested profile. It is idempotent: an artifact that already satisfies the
// profile is left untouched, so a retry after canonicalization does not
// re-encode (and re-hash) it.
//
// The rewrite is atomic: ffmpeg writes a sibling temp file, the temp file is
// probed and certified, and only then is it renamed over the artifact. A
// failed encode therefore leaves the original bytes untouched, and no other
// process can observe a half-written MP4.
func canonicalizeOutputMP4(ctx context.Context, path, profileID string) (bool, error) {
	profile, err := media.ResolveProfile(profileID)
	if err != nil {
		return false, err
	}
	existing, err := media.ProbeFile(ctx, path)
	if err != nil {
		return false, fmt.Errorf("probe source: %w", err)
	}
	if err := profile.ValidateProbe(existing); err == nil {
		return false, nil
	}

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
	if profile.CodecConfigSHA256 != "" && probe.CodecConfigSHA256 != profile.CodecConfigSHA256 {
		return false, fmt.Errorf("canonical output avcC %q, want %q", probe.CodecConfigSHA256, profile.CodecConfigSHA256)
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

// writeCanonicalOverlayReceipt writes a receipt for the canonicalized bytes.
// It is written BEFORE the rename (while the temp file still exists) so the
// receipt path is already correct when the artifact becomes visible; if the
// rename fails, the receipt still describes the temp path's would-be bytes and
// the job fails closed.
func writeCanonicalOverlayReceipt(path string, probe media.ProbeResult, sha string, size int64, probeMS, hashMS float64) error {
	totalMS := probeMS + hashMS
	receipt := chronon.MediaReceipt{Schema: canonicalOverlayReceiptSchema}
	receipt.Output.Bytes = size
	receipt.Output.SHA256 = sha
	receipt.Verification.Status = "pass"
	receipt.Verification.RequestedPolicy = chronon.ReceiptVerifyFast
	receipt.Verification.ResolvedPolicy = chronon.ReceiptVerifyFast
	receipt.Verification.FFprobe = "pass"
	receipt.Verification.FrameCount = "pass"
	receipt.Verification.Codec = "pass"
	receipt.Verification.PixelFormat = "pass"
	receipt.Verification.Resolution = "pass"
	receipt.Verification.FPS = "pass"
	receipt.Verification.Audio = "pass"
	// No decode ran here. This is honest and makes every decode-promising
	// worker policy run its own gate on the final bytes.
	receipt.Verification.Decode = "skip"
	receipt.Timing.FFProbeMS = &probeMS
	receipt.Timing.SHA256MS = &hashMS
	receipt.Timing.TotalMS = &totalMS

	m := &receipt.Media
	m.Container = probe.Container
	m.Codec = "h264"
	m.VideoCodec = probe.VideoCodec
	m.CodecProfile = probe.CodecProfile
	m.VideoLevel = probe.VideoLevel
	m.PixelFormat = probe.PixelFormat
	m.Width = probe.Width
	m.Height = probe.Height
	m.FPSNum = probe.FPSNum
	m.FPSDen = probe.FPSDen
	m.RFPSNum = probe.FPSNum
	m.RFPSDen = probe.FPSDen
	m.FrameCount = int64(probe.FrameCount)
	m.DurationUS = probe.DurationUS
	m.DurationMS = float64(probe.DurationUS) / 1000
	m.HasAudio = probe.AudioStreams > 0
	m.HasVideo = probe.HasVideo
	m.VideoStreams = probe.VideoStreams
	m.VideoTimeBaseNum = probe.VideoTimeBaseNum
	m.VideoTimeBaseDen = probe.VideoTimeBaseDen
	m.SARNum = probe.SARNum
	m.SARDen = probe.SARDen
	m.ColorRange = probe.ColorRange
	m.ColorSpace = probe.ColorSpace
	m.ColorTransfer = probe.ColorTransfer
	m.ColorPrimaries = probe.ColorPrimaries
	m.FieldOrder = probe.FieldOrder
	m.StartPTS = probe.StartPTS
	m.FirstFrameKeyframe = probe.FirstFrameKeyframe
	m.ClosedGOP = probe.ClosedGOP
	m.KeyframeInterval = probe.KeyframeInterval
	m.AudioStreams = probe.AudioStreams
	m.AudioCodec = probe.AudioCodec
	m.AudioProfile = probe.AudioProfile
	m.SampleRate = probe.SampleRate
	m.Channels = probe.Channels
	m.ChannelLayout = probe.ChannelLayout
	m.AudioBitrate = probe.AudioBitrate
	m.AudioTimeBaseNum = probe.AudioTimeBaseNum
	m.AudioTimeBaseDen = probe.AudioTimeBaseDen

	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode canonical receipt: %w", err)
	}
	receiptPath := path + chronon.MediaReceiptSuffix
	tempReceipt := receiptPath + ".tmp"
	if err := os.WriteFile(tempReceipt, data, 0o644); err != nil {
		return fmt.Errorf("write canonical receipt: %w", err)
	}
	if err := os.Rename(tempReceipt, receiptPath); err != nil {
		_ = os.Remove(tempReceipt)
		return fmt.Errorf("publish canonical receipt: %w", err)
	}
	return nil
}
