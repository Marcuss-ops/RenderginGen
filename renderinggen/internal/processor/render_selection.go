// render_selection.go derives transport-neutral job facts: chunk frame ranges,
// authored visual-layer presence, and the workspace path of the declared
// master audio source. Chronon owns the physical execution-path decision.
package processor

import (
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// jobFrameRange maps a job's half-open chunk contract [Start, End) onto
// Chronon's inclusive last-frame coordinate. ok is false when the job has no
// frame range (render the whole plan). The boolean is load-bearing: a chunk
// covering exactly frame 0 (Start=0, End=1) yields first=0, last=0, which is
// indistinguishable from "no range" without the explicit presence signal.
func jobFrameRange(job *queue.Job) (first, last int64, ok bool) {
	if job != nil && job.FrameRange != nil {
		return job.FrameRange.Start, job.FrameRange.End - 1, true
	}
	return 0, 0, false
}

// planHasVisualOverlay identifies whether the semantic plan contains an
// authored visual layer in addition to source video. This is a requirement
// fact sent to Chronon; it is deliberately not a DirectYUV/FullGraph choice.
func planHasVisualOverlay(plan *overlay.Plan) bool {
	if plan == nil {
		return false
	}
	videoLayers := 0
	for _, layer := range plan.Layers {
		if layer.Type == "video" {
			videoLayers++
			continue
		}
		if layer.Type != "" && layer.Type != "video" {
			return true
		}
	}
	// DirectYUV can feed one decoded source straight to NVENC. Two video
	// layers require a compositor (for example clip-render's background plus
	// foreground); otherwise Chronon may select DirectYUV and reject the
	// second source at frame zero.
	return videoLayers > 1
}

func planHasVideoSource(plan *overlay.Plan) bool {
	if plan == nil {
		return false
	}
	for _, layer := range plan.Layers {
		if layer.Type == "video" {
			return true
		}
	}
	return false
}

// audioModeCopyOnly reports whether an audio mode is one the worker's mux can
// actually honour. The native A/V path copies the source stream
// (`--gop-source`) and never transcodes, so a mode that promises a transcode
// is a request the worker cannot fulfil; it must degrade loudly, never
// silently render audio the caller did not ask for.
//
// The accepted set is deliberately the copy family only. It is exported
// through the returned warning flag so the caller records a metric: the
// semantic contract legitimately carries modes such as "transcode"
// (PipelineGen clip plans do), and rejecting them at compile time would break
// the accepted contract — silently ignoring them would be worse.
func audioModeCopyOnly(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "copy", "copy_if_compatible", "passthrough", "mux":
		return true
	default:
		return false
	}
}

// audioSourcePathFromPlan is the single typed authority for master audio.
// It reads Plan.Output.Audio (mode) and the source layer's ALREADY-RESOLVED
// logical path from the compiled plan, so there is exactly one place where
// audio policy is interpreted and one owner of the asset path (the compiler's
// asset registry). sample_rate/channels/codec are surfaced as inert warnings
// via the returned warning flag so the caller can metric/log them.
//
// The source path is deliberately NOT rebuilt locally as
// assets/semantic/<asset_id>.mp4: that shortcut skipped the registry's id
// sanitization and media-type extension rules, so an asset id containing a
// space or slash produced a path the registry never wrote.
func audioSourcePathFromPlan(plan *overlay.Plan, workspaceRoot string) (string, bool) {
	if plan == nil || plan.Output.Audio == nil {
		return "", false
	}
	// Inert audio transcode params: Chronon's current mux copies the source
	// stream; sample_rate/channels/codec never affect rendering but callers
	// set them expecting a transcode. Surface as warning/metric. The MODE is
	// part of the same degradation: a plan whose only audio directive is
	// {"mode":"transcode"} used to produce no warning at all, so the worker
	// silently copied where the caller asked for a transcode.
	audio := plan.Output.Audio
	warnInert := !audioModeCopyOnly(audio.Mode) || audio.Codec != "" || audio.SampleRate != 0 || audio.Channels != 0
	if audio.Mode == "" {
		return "", warnInert
	}
	// The compiler emits the source clip as the layer with id "source"; its
	// Source field is the registry-resolved logical path.
	for _, layer := range plan.Layers {
		if layer.ID == "source" && layer.Source != "" {
			return filepath.Join(workspaceRoot, filepath.FromSlash(layer.Source)), warnInert
		}
	}
	return "", warnInert
}
