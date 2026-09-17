// finalize_job.go owns the CPU-bound post half of the staged pipeline:
// validation probes, profile certification and delegation to the store phase.
package processor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// probeSource names where the finalized artifact's structural facts came from.
//
// It exists so the rule "the receipt is the authority, the local probe is the
// fallback" is a value a test can assert, instead of a branch inside a 100-line
// function that only an ffprobe's wall time could observe.
type probeSource string

const (
	// probeSourceReceipt: Chronon's canonical receipt described the media, so the
	// worker did NOT spawn ffprobe again on the artifact it just produced.
	probeSourceReceipt probeSource = "receipt"
	// probeSourceLocalProbe: the receipt could not describe the media (absent,
	// not canonical, unreadable), so the worker inspected the file itself.
	probeSourceLocalProbe probeSource = "local_ffprobe"
)

// probeFactsForFinalize resolves the artifact's structural facts from the
// canonical receipt, falling back to a LOCAL ffprobe only when the receipt
// cannot describe the media.
//
// This is the single decision point for the duplicate-verification cost: the
// engine already read the bytes while encoding, so the worker paying a second
// ffprobe per job (~250 ms measured) is pure waste on the hot path. The fallback
// is not dead code — it is what keeps the worker correct for a renderer that did
// not write a receipt — but it must never be the normal path.
func probeFactsForFinalize(ctx context.Context, receipt chronon.MediaReceipt, receiptErr error, outputPath string) (media.ProbeResult, probeSource, error) {
	if receiptErr == nil && receipt.HasCanonicalMedia() {
		return probeResultFromReceipt(receipt), probeSourceReceipt, nil
	}
	probed, err := media.ProbeFile(ctx, outputPath)
	if err != nil {
		return media.ProbeResult{}, probeSourceLocalProbe, err
	}
	return probed, probeSourceLocalProbe, nil
}

// probeResultFromReceipt maps Chronon's canonical media receipt facts into a media.ProbeResult,
// avoiding duplicate ffprobe process invocations on the critical finalization path.
func probeResultFromReceipt(r chronon.MediaReceipt) media.ProbeResult {
	m := r.Media
	durUS := m.DurationUS
	if durUS <= 0 && m.DurationMS > 0 {
		durUS = int64(m.DurationMS * 1000)
	}
	videoCodec := m.VideoCodec
	if videoCodec == "" {
		videoCodec = m.Codec
	}
	fpsNum := m.RFPSNum
	fpsDen := m.RFPSDen
	if fpsNum <= 0 {
		fpsNum = m.FPSNum
		fpsDen = m.FPSDen
	}
	if fpsDen <= 0 {
		fpsDen = 1
	}
	return media.ProbeResult{
		Container:              m.Container,
		DurationUS:             durUS,
		Width:                  m.Width,
		Height:                 m.Height,
		FPSNum:                 fpsNum,
		FPSDen:                 fpsDen,
		PixelFormat:            m.PixelFormat,
		VideoCodec:             videoCodec,
		CodecProfile:           m.CodecProfile,
		FrameCount:             int(m.FrameCount),
		FirstFrameKeyframe:     m.FirstFrameKeyframe,
		HasVideo:               m.HasVideo || m.Width > 0,
		VideoStreams:           m.VideoStreams,
		VideoLevel:             m.VideoLevel,
		VideoTimeBaseNum:       m.VideoTimeBaseNum,
		VideoTimeBaseDen:       m.VideoTimeBaseDen,
		AudioTimeBaseNum:       m.AudioTimeBaseNum,
		AudioTimeBaseDen:       m.AudioTimeBaseDen,
		SARNum:                 m.SARNum,
		SARDen:                 m.SARDen,
		ColorRange:             m.ColorRange,
		ColorSpace:             m.ColorSpace,
		ColorTransfer:          m.ColorTransfer,
		ColorPrimaries:         m.ColorPrimaries,
		FieldOrder:             m.FieldOrder,
		StartPTS:               m.StartPTS,
		KeyframeInterval:       m.KeyframeInterval,
		AudioStreams:           m.AudioStreams,
		HasAudio:               m.HasAudio,
		AudioCodec:             m.AudioCodec,
		AudioProfile:           m.AudioProfile,
		SampleRate:             m.SampleRate,
		Channels:               m.Channels,
		ChannelLayout:          m.ChannelLayout,
		AudioBitrate:           m.AudioBitrate,
		ClosedGOP:              m.ClosedGOP,
		ClosedGOPUncertifiable: false,
		FPSUncertifiable:       fpsNum <= 0 || fpsDen <= 0,
	}
}

// requiresStructuralProbe decides which finalized jobs are structurally probed.
// overlay.render jobs are probed because their media contract is validated from
// the probe; jobs carrying a profile_id because the output profile is validated
// from it; and ANY job carrying a FrameRange because a chunk is a potential
// parent-assembly input: the queue's copy-safety gate (queue.ValidateChildren)
// validates a copied child from exactly these facts, so a chunk that skipped
// the probe could never be proven safe to concatenate.
func requiresStructuralProbe(job *queue.Job) bool {
	return job != nil && (job.JobType == queue.JobTypeOverlayRender || job.FrameRange != nil)
}

// FinalizeJob runs the CPU-bound post half of the render pipeline: validation
// probes, output hashing (Chronon receipt first), object-store upload and the
// artifact ledger row.
func (p *Processor) FinalizeJob(ctx context.Context, prepared *PreparedJob) (queue.Artifact, error) {
	job := prepared.Job
	metrics := prepared.Metrics
	outputPath := prepared.OutputPath
	plan := prepared.Plan
	metadata := planMetadataOf(plan)
	receipt, receiptErr := chronon.ReadMediaReceipt(outputPath)
	var probe *media.ProbeResult
	if requiresStructuralProbe(job) || metadata.ProfileID != "" {
		probeStart := time.Now()
		probed, source, err := probeFactsForFinalize(ctx, receipt, receiptErr, outputPath)
		if err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: overlay ffprobe: %w", err)
		}
		if source != probeSourceReceipt {
			// Not fatal, but never silent: a job whose facts came from a local
			// probe is paying the duplicate verification the receipt exists to
			// avoid, and that is worth seeing in the log.
			log.Printf("[processor] media facts for %s came from %s (no canonical receipt): the artifact was inspected locally", job.ID, source)
		}
		probeUS := float64(time.Since(probeStart).Microseconds())
		metrics[metricnames.ProbeUS] = probeUS
		metrics[metricnames.ProbeMS] = probeUS / 1000

		if metadata.ProfileID != "" {
			profile, err := media.ResolveProfile(metadata.ProfileID)
			if err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: output profile: %w", err)
			}
			if err := profile.ValidateProbe(probed); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: output profile certification: %w", err)
			}
		} else if job.JobType == queue.JobTypeOverlayRender {
			if err := probed.ValidateOverlay(metadata.Width, metadata.Height, metadata.FPSNum, metadata.FPSDen); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: overlay media contract: %w", err)
			}
		}
		if planHasVisualOverlay(plan) && p.deepVisualValidationEnabled() {
			if err := probed.ValidateVisible(ctx, outputPath); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: visual output gate: %w", err)
			}
		}
		probe = &probed
	}
	// Media decodability is verified by Chronon — the canonical verifier of
	// the artifact Chronon itself produced — inside its receipt
	// (normal/certify run the full decode passes there; fast skips re-decode
	// by design). RenderingGen requests the policy and enforces the receipt
	// result; it never re-decodes the output a second time. This runs for
	// every finalized job, independent of overlay/profile probing.
	// One gate, two verdicts: the receipt policy AND the artifact's byte
	// identity. The store phase below consumes both rather than repeating the
	// identity decision, so a hash that does not match the bytes is rejected at
	// every policy that promises proof instead of becoming the artifact's
	// permanent content address.
	outcome, err := p.verifyArtifactReceipt(outputPath, receipt, receiptErr, metrics)
	if err != nil {
		return queue.Artifact{}, err
	}
	return p.storeArtifact(ctx, job.ID, outputPath, outcome, plan, metrics, prepared.totalStart, probe, prepared.Stats, prepared.InputBytes,
		requiresStructuralProbe(job) || metadata.ProfileID != "", prepared.NativeCertified)
}
