// render_selection.go classifies a job/plan onto a render path: chunk frame
// ranges, DirectYUV eligibility (video-only) versus authored composition, and
// the workspace path of the declared master audio source.
package processor

import (
	"encoding/json"
	"path/filepath"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
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

// planHasVisualOverlay identifies concrete plans that require Chronon's
// authored composition graph. A video-only plan can use DirectYUV; an
// image/text/color plan must use native composition even when it has no
// separate background.
func planHasVisualOverlay(plan *overlay.Plan) bool {
	if plan == nil {
		return false
	}
	for _, layer := range plan.Layers {
		if layer.Type == "video" {
			continue
		}
		if layer.Type != "" && layer.Type != "video" {
			return true
		}
	}
	// DirectYUV supports the authored multi-video base/overlay path. Keep that
	// fast path eligible; only non-video authored layers require the graph.
	return false
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

// audioSourcePathFromSemantic resolves the declared master audio source to
// the worker workspace. Audio is semantic metadata and is intentionally not
// serialized into Chronon's visual render-plan.v2; the native encoder still
// needs the materialized source path to mux the audio stream.
func audioSourcePathFromSemantic(raw []byte, workspaceRoot string) string {
	var doc struct {
		Audio *struct {
			Mode string `json:"mode"`
		} `json:"audio"`
		Source *struct {
			AssetID string `json:"asset_id"`
			Path    string `json:"path"`
		} `json:"source"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Audio == nil || doc.Source == nil || doc.Source.AssetID == "" {
		return ""
	}
	path := doc.Source.Path
	if path == "" {
		path = filepath.ToSlash(filepath.Join("assets", "semantic", doc.Source.AssetID+".mp4"))
	}
	return filepath.Join(workspaceRoot, filepath.FromSlash(path))
}
