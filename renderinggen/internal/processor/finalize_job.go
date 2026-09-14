// finalize_job.go owns the CPU-bound post half of the staged pipeline:
// validation probes, profile certification and delegation to the store phase.
package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

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
	if job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "" {
		probeStart := time.Now()
		var probed media.ProbeResult
		var err error
		if receiptErr == nil && receipt.HasCanonicalMedia() {
			probed = probeResultFromReceipt(receipt)
		} else {
			probed, err = media.ProbeFile(ctx, outputPath)
			if err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: overlay ffprobe: %w", err)
			}
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
		if planHasVisualOverlay(plan) && deepVisualValidationEnabled() {
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
	if err := p.enforceReceiptVerificationDirect(receipt, receiptErr, metrics); err != nil {
		return queue.Artifact{}, err
	}
	return p.storeArtifact(ctx, job.ID, outputPath, plan, metrics, prepared.totalStart, probe, prepared.Stats, prepared.InputBytes,
		job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "", prepared.NativeCertified)
}
