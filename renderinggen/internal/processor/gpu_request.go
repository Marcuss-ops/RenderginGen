// gpu_request.go owns the single Chronon request builder. It is a pure
// projection of a PreparedJob plus a RenderExecutionPolicy: no I/O, no
// goroutines and no tail-upload side effects.
package processor

import (
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

// BuildChrononRequest projects the resolved policy onto the engine boundary.
// It performs no I/O and does not mutate the prepared job or plan; the
// LaneTiming callback is the only deferred metric update and the renderer
// invokes it. The policy is the only source for backend, encoder, requirements
// and range; this function never re-derives them.
func (p *Processor) BuildChrononRequest(prepared *PreparedJob, policy RenderExecutionPolicy, progress func(chronon.RenderProgress)) (chronon.RenderRequest, error) {
	if prepared == nil || prepared.Job == nil || prepared.Workspace == nil {
		return chronon.RenderRequest{}, fmt.Errorf("processor: build render request requires a prepared job and workspace")
	}
	if err := policy.Validate(); err != nil {
		return chronon.RenderRequest{}, err
	}
	metadata := planMetadataOf(prepared.Plan)
	return chronon.RenderRequest{
		PlanPath:            prepared.Workspace.PlanPath(),
		PreparedPackagePath: prepared.Workspace.PreparedPackagePath(),
		// Plans use the canonical assets/<file> namespace, so the workspace
		// root (not root/assets) is Chronon's mounted root.
		AssetsRoot:            prepared.Workspace.Root(),
		OutputPath:            prepared.OutputPath,
		AudioSourcePath:       prepared.AudioSourcePath,
		AudioTargetSampleRate: prepared.AudioTargetSampleRate,
		Report:                p.report,
		EncodePreset:          policy.EncodePreset,
		HardwareEncoder:       policy.HardwareEncoder,
		EncoderBackend:        policy.EncoderBackend,
		ReceiptVerify:         p.chrononReceiptRequest(),
		LaneTiming: func(idle time.Duration) {
			prepared.Metrics[metricnames.DaemonLaneIdleUS] = float64(idle.Microseconds())
		},
		Requirements: chronon.ExecutionRequirements{
			Backend:             policy.Backend,
			GPURequired:         policy.GPURequired,
			CPUFallbackAllowed:  policy.CPUFallbackAllowed,
			CompositionRequired: policy.CompositionRequired,
			VideoSourceRequired: policy.VideoSourceRequired,
			PacketCopyAllowed:   true,
		},
		FirstFrame:       policy.FrameRange.First,
		LastFrame:        policy.FrameRange.Last,
		RangeEnabled:     policy.FrameRange.Enabled,
		ParallelDisjoint: policy.ParallelDisjoint,
		Output: chronon.OutputSpec{
			Codec:          "h264",
			Width:          uint32(metadata.Width),
			Height:         uint32(metadata.Height),
			FPSNum:         uint32(metadata.FPSNum),
			FPSDen:         uint32(metadata.FPSDen),
			PipePixFmt:     p.pipePixFmt,
			EncoderProfile: canonicalEncoderProfileFor(metadata, prepared.AudioSourcePath != ""),
			FragmentedMP4:  prepared.FragmentedMP4,
		},
		OutputSegments: prepared.Segments,
		TotalFrames:    int64(metadata.FrameCount),
		Progress:       progress,
	}, nil
}
