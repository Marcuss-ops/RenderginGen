// canonical_receipt.go owns the RECEIPT of the canonical output: the schema
// that marks bytes as final, the identity check that permits skipping the
// pass, the forced-encode decision, and the receipt written after the temp
// artifact is certified. It does not decide eligibility or build ffmpeg args.
package processor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// canonicalOverlayReceiptSchema names the receipt producer explicitly. The
// bytes are final after this pass, so a consumer must not read the pre-pass
// Chronon receipt; this schema is the marker that the media facts and hash
// describe the canonicalized artifact.
const canonicalOverlayReceiptSchema = "renderinggen.canonical-overlay-receipt.v1"

// canonicalRendererReceiptSchema is the only renderer-owned receipt schema a
// canonical skip may trust. Any other schema (including an older canonical
// receipt version) is not proof about the current avcC contract.
const canonicalRendererReceiptSchema = "chronon3d.render-receipt.v1"

func canonicalReceiptSchemaAccepted(schema string) bool {
	return schema == canonicalOverlayReceiptSchema || schema == canonicalRendererReceiptSchema
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
	if err != nil || !canonicalReceiptSchemaAccepted(receipt.Schema) {
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
		// Best-effort cleanup of the unpublished temp receipt; the rename error
		// is the returned failure.
		_ = os.Remove(tempReceipt)
		return fmt.Errorf("publish canonical receipt: %w", err)
	}
	return nil
}
