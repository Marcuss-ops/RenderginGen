// gpu_run.go owns the orchestration of the GPU half: resolve the policy, start
// the tail upload, build the request, execute one session and certify/hand off.
// The stages live in gpu_request.go, gpu_progress.go, gpu_session.go and
// gpu_execution_policy.go.
package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// progressLogInterval bounds how often a render's frame milestones reach the
// log. Every milestone still updates the progress tracker and the stall
// heartbeat; only the log line is sampled. The first and final milestone are
// always logged.
const progressLogInterval = 10 * time.Second

// renderHeartbeatInterval is how long a render may go without a frame
// milestone before the worker emits an elapsed-time heartbeat. The daemon IPC
// transport executes RENDER_JOB in one blocking call and never streams frame
// milestones.
const renderHeartbeatInterval = 15 * time.Second

// progressSummary renders a frame position as a percentage plus a linear ETA.
// A renderer that prints no milestones reports "unknown" instead of pretending
// to be at 0%.
func progressSummary(done, total int64, fps float64) string {
	if total <= 0 {
		return "unknown"
	}
	percent := float64(done) / float64(total) * 100
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	summary := fmt.Sprintf("%.1f%%", percent)
	if fps > 0 && done < total {
		remaining := time.Duration(float64(total-done)/fps) * time.Second
		summary += " eta=" + remaining.Round(time.Second).String()
	}
	return summary
}

// RunGPU performs one render session for a prepared job plus the strict-backend
// receipt gate. It is an orchestration: every decision comes from
// RenderExecutionPolicy and every stage owns its own state.
func (p *Processor) RunGPU(ctx context.Context, prepared *PreparedJob) error {
	if prepared == nil || prepared.Job == nil {
		return fmt.Errorf("processor: RunGPU requires a prepared job")
	}
	policy, err := ResolveExecutionPolicy(prepared.Job, prepared.Plan, p.executionPolicyConfig(prepared))
	if err != nil {
		return err
	}
	if policy.CompositionRequired && !policy.VideoSourceRequired {
		// Chronon owns the physical choice: Vulkan composition followed by a
		// host-frame pipe handoff, distinct from the native source/NVENC lane.
		prepared.Metrics[metricnames.ChrononGPUCompositionPipe] = 1
	}
	metadata := planMetadataOf(prepared.Plan)
	workerlog.ByJobID(prepared.Job.ID).Statef("rendering", "canvas=%dx%d fps=%d/%d frames=%d backend=%s encoder=%s gpu_required=%t",
		metadata.Width, metadata.Height, metadata.FPSNum, metadata.FPSDen, metadata.FrameCount,
		policy.Backend, policy.HardwareEncoder, policy.GPURequired)
	tail := p.startTailUploadIfEligible(ctx, prepared, metadata)
	observer := newRenderProgressObserver(prepared.Job, p.progressTracker, policy.Backend, policy.HardwareEncoder)
	request, err := p.BuildChrononRequest(prepared, policy, observer.Observe)
	if err != nil {
		abortTail(tail)
		return err
	}
	stopHeartbeat := observer.Heartbeat(ctx)
	defer stopHeartbeat()
	prepared.baseRenderRequest = request
	outcome, err := p.ExecuteRenderSession(ctx, prepared, request, policy)
	if err != nil {
		abortTail(tail)
		return err
	}
	ownership, err := p.CertifyAndHandoff(prepared, policy, outcome, observer, tail)
	if err != nil {
		abortTail(tail)
		return err
	}
	if ownership.Commit {
		prepared.TailUpload = ownership.Upload
	} else {
		abortTail(tail)
	}
	return nil
}

// startTailUploadIfEligible starts the streaming upload exactly when the
// artifact is a fragmented overlay, the store can stage and the job is not a
// segmented or canonicalized lane.
func (p *Processor) startTailUploadIfEligible(ctx context.Context, prepared *PreparedJob, metadata planMetadata) *stagedUpload {
	if !prepared.FragmentedMP4 || !p.store.SupportsStaging() || len(prepared.Segments) > 0 ||
		isCanonicalizedOutputProfile(metadata.ProfileID) {
		return nil
	}
	return p.startTailUpload(ctx, prepared.Job.ID, prepared.OutputPath)
}

// abortTail discards a pending upload. It is nil-safe and a no-op once Finish
// committed the stream.
func abortTail(tail *stagedUpload) {
	if tail != nil {
		tail.Abort()
	}
}
