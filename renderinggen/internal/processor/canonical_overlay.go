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
//
// Placement: the pass is CPU-only and runs in the post pool (FinalizeJob),
// after the GPU lane has been released. Running it inside RunGPU held a render
// device while re-encoding an artifact that was already finished.
package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
// at least one artifact was re-encoded. The pass is CPU-only and runs in the
// post pool (FinalizeJob), not on the GPU lane.
//
// A segmented batch encodes the same timeline twice (mirror + per-item
// segments): both are separate deliverables and the canonical avcC is an x264
// identity the NVENC session cannot emit. Deriving the mirror by packet-copy
// concat does remove the second encode, but it breaks the uniform 48-frame GOP
// at every seam. Measured on the dev host: the concat still certifies V3, but
// its closed-GOP cadence is lost, and downstream copy gates derive
// copy-readiness from the packet table (closed_gop, keyframe interval). The
// duplicate is therefore a CONTRACT trade, not an implementation accident:
// removing it needs either the renderer to emit the canonical x264 stream in
// one session (Chronon) or a downstream policy that accepts a non-uniform
// cadence. Do not "fix" it with a concat without that decision. The Chronon
// side is tracked in
// Chronon3d/docs/tickets/TICKET-CANONICAL-X264-SINGLE-SESSION-20261116.md.
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

// canonicalizeConcurrency bounds how many canonical encodes run at once inside
// ONE job. The pass is CPU-only x264 work; the post pool already runs several
// jobs concurrently, so an unbounded fan-out over a large batch would starve
// the probes, hashing and uploads that share the same CPUs. Two lanes shorten
// the per-job tail without turning one batch into a process storm.
const canonicalizeConcurrency = 2

// canonicalizeOutputPaths canonicalizes an explicit set of outputs. The P4
// single-segment retry uses it for the one segment it re-rendered. Paths are
// deduplicated, encoded on a bounded pool, and inspected in input order, so a
// batch's output set is deterministic regardless of which encode finished
// first.
func (p *Processor) canonicalizeOutputPaths(ctx context.Context, jobID string, paths []string, metrics map[string]float64, profileID string) (bool, error) {
	unique := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	if len(unique) == 0 {
		return false, nil
	}

	type canonicalizeResult struct {
		encoded bool
		err     error
	}
	results := make([]canonicalizeResult, len(unique))
	workers := len(unique)
	if workers > canonicalizeConcurrency {
		workers = canonicalizeConcurrency
	}
	passStart := time.Now()
	work := make(chan int)
	var wg sync.WaitGroup
	for lane := 0; lane < workers; lane++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range work {
				if err := ctx.Err(); err != nil {
					results[index] = canonicalizeResult{err: err}
					continue
				}
				encoded, err := canonicalizeOutputMP4(ctx, unique[index], profileID, p.acceptedCodecConfigSHA256...)
				results[index] = canonicalizeResult{encoded: encoded, err: err}
			}
		}()
	}
	for index := range unique {
		work <- index
	}
	close(work)
	wg.Wait()
	wall := time.Since(passStart)

	changed := false
	encodedCount := 0
	var failures []error
	for index, result := range results {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("canonicalize %s: %w", filepath.Base(unique[index]), result.err))
			continue
		}
		if result.encoded {
			changed = true
			encodedCount++
		}
	}
	if changed {
		if metrics != nil {
			metrics[metricnames.CanonicalizeOverlayUS] = float64(wall.Microseconds())
			metrics[metricnames.CanonicalizeOverlayMS] = float64(wall.Microseconds()) / 1000
		}
		workerlog.ByJobID(jobID).Infof("canonical output emitted: %d/%d artifact(s) re-encoded to the canonical avcC (profile %s, wall=%.0fms)",
			encodedCount, len(unique), profileID, float64(wall.Microseconds())/1000)
	}
	if len(failures) > 0 {
		return changed, errors.Join(failures...)
	}
	return changed, nil
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

// receiptCertifiesCanonicalIdentity reports whether the receipt next to path
// certifies the canonical parameter sets for the file on disk.
//
// This is the last duplicate verification the canonical lane pays in the
// finalize. Chronon's receipt carries media.codec_config_sha256 — the SHA-256
// of the avcC it emitted — and the worker's own canonical pass writes the same
// field after hashing the bytes it renamed into place. A passing receipt whose
// avcC equals the profile's pin and whose byte size equals the file's is a
// claim about THESE bytes; probing them again cannot change the decision.
//
// It is deliberately narrow: no receipt, a failing receipt, a digest-less
// receipt, a different avcC or a size mismatch all fall through to the probe
// and (when needed) the encode, so the pass stays fail-closed and idempotent.
func receiptCertifiesCanonicalIdentity(path, canonicalCodecConfig string) bool {
	if canonicalCodecConfig == "" {
		return false
	}
	receipt, err := chronon.ReadMediaReceipt(path)
	if err != nil {
		return false
	}
	if !receipt.VerificationPassed() || receipt.Output.SHA256 == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(receipt.Media.CodecConfigSHA256), canonicalCodecConfig) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return receipt.Output.Bytes > 0 && receipt.Output.Bytes == info.Size()
}

// needsCanonicalEncodeFromRenderReceipt reports whether a render receipt makes
// the source probe unnecessary because the encode is unconditionally required.
//
// It is deliberately conservative: only a PASSING renderer receipt (never the
// worker's own canonical receipt, which describes already-final bytes) plus an
// empty allowlist takes the shortcut. Everything else keeps the probe, so the
// pass stays idempotent for retries and for recertified native artifacts.
func needsCanonicalEncodeFromRenderReceipt(path, canonicalCodecConfig string, hasAllowlist bool) bool {
	if hasAllowlist {
		return false
	}
	receipt, err := chronon.ReadMediaReceipt(path)
	if err != nil || receipt.Schema == canonicalOverlayReceiptSchema {
		return false
	}
	if !receipt.VerificationPassed() || receipt.Output.SHA256 == "" {
		return false
	}
	// A renderer that already certifies the canonical parameter sets is not a
	// forced re-encode: fall back to the probe, which confirms the bytes and
	// skips the pass when they match. This is the bridge a single-session
	// canonical Chronon render needs; without it the shortcut would re-encode
	// the very artifact that lane exists to produce.
	if canonicalCodecConfig != "" && receipt.Media.CodecConfigSHA256 == canonicalCodecConfig {
		return false
	}
	return true
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
	m.CodecConfigSHA256 = probe.CodecConfigSHA256
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
