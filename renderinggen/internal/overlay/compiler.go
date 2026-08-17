// Package overlay owns the RenderingGen-side boundary between PipelineGen and
// Chronon. RenderingGen is an EXECUTION WORKER: it validates a job, materializes
// its assets, writes plan.json, renders with Chronon and publishes the artifact.
// It does NOT make visual decisions.
//
// The editorial semantic_role → Chronon visual-preset decision is owned by
// PipelineGen's SemanticOverlayResolver, which emits a concrete
// chronon.render-plan.v1 document. RenderingGen accepts that document unchanged
// and rejects a semantic overlay-plan.v1 — a semantic plan that reaches the
// worker means PipelineGen skipped its compile step, which is a wiring bug, not
// something the worker should silently fix by re-deriving a second Chronon.
package overlay

import (
	"encoding/json"
	"fmt"
)

// SemanticSchema is PipelineGen's semantic overlay contract. RenderingGen does
// not compile it; PipelineGen owns that step.
const SemanticSchema = "renderinggen.overlay-plan.v1"

// Plan is the concrete chronon.render-plan.v1 document the worker executes.
// It is carried here so callers/tests can decode a submitted plan; the worker
// itself treats it as an opaque blob passed straight to Chronon.
type Plan struct {
	Schema       string  `json:"schema"`
	Version      int     `json:"version"`
	JobID        string  `json:"job_id"`
	StyleProfile string  `json:"style_profile,omitempty"`
	Canvas       Canvas  `json:"canvas"`
	Layers       []Layer `json:"layers"`
	Output       Output  `json:"output"`
}

type Canvas struct {
	Width          int   `json:"width"`
	Height         int   `json:"height"`
	FPS            int   `json:"fps"`
	DurationFrames int64 `json:"duration_frames"`
}

type Layer struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Asset string `json:"asset,omitempty"`
	// Source is the video-source logical path for Video layers
	// (VIDEO_BACKGROUND): Chronon video layers reference `source`.
	Source         string    `json:"source,omitempty"`
	Text           string    `json:"text,omitempty"`
	Preset         string    `json:"preset,omitempty"`
	Font           string    `json:"font,omitempty"`
	FontSize       float64   `json:"font_size,omitempty"`
	BoxWidth       int       `json:"box_width,omitempty"`
	BoxHeight      int       `json:"box_height,omitempty"`
	Fit            string    `json:"fit,omitempty"`
	Position       []float64 `json:"position,omitempty"`
	Color          []float64 `json:"color,omitempty"`
	Opacity        float64   `json:"opacity,omitempty"`
	BlendMode      string    `json:"blend_mode,omitempty"`
	Loop           bool      `json:"loop,omitempty"`
	StartFrame     int64     `json:"start_frame"`
	DurationFrames int64     `json:"duration_frames"`
	// Animation is the motion preset applied to the layer.
	Animation *LayerAnimation `json:"animation,omitempty"`
}

// LayerAnimation mirrors the chronon.render-plan.v1 layer animation block.
type LayerAnimation struct {
	Preset string `json:"preset"`
}

type Output struct {
	Path      string `json:"path"`
	Format    string `json:"format"`
	Codec     string `json:"codec"`
	ProfileID string `json:"profile_id,omitempty"`
}

type Asset struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
}

// CompileIfSemantic returns a concrete chronon.render-plan.v1 document
// unchanged. A PipelineGen semantic plan (overlay-plan.v1) is rejected: it must
// be compiled by PipelineGen (SemanticOverlayResolver) before submission. The
// worker never re-derives presets, geometry, layout or font — those are
// PipelineGen's and Chronon's responsibilities, not RenderingGen's.
//
// The `semantic` return is retained for the call sites that already ignore it;
// it is always false: a semantic plan is an error, never silently compiled.
func CompileIfSemantic(raw []byte) ([]byte, []Asset, bool, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, false, fmt.Errorf("overlay: decode plan: %w", err)
	}
	if probe.SchemaVersion == "" {
		// Concrete chronon.render-plan.v1 → pass through untouched.
		return raw, nil, false, nil
	}
	if probe.SchemaVersion == SemanticSchema {
		return nil, nil, false, fmt.Errorf(
			"overlay: semantic %q plan must be compiled by PipelineGen (SemanticOverlayResolver) before rendering", probe.SchemaVersion)
	}
	return nil, nil, false, fmt.Errorf("overlay: unsupported plan schema %q", probe.SchemaVersion)
}
