// Package overlaybatch runs a data-driven overlay batch end to end: read the
// batch manifest, submit it to the central queue, wait every job terminal,
// download the certified artifacts (and their raw timing sidecars), and write
// the run report.
//
// Why it exists. The producer-side run loop used to live in per-corpus Python
// scripts that re-implemented the queue HTTP contract — submit, poll, download,
// hash — beside the Go client that already owns it. Both were removed when this
// package landed (mike_tyson_overlay_test/preset_overlays_v1/scripts/ and
// multilingual_overlays_v1/scripts/ no longer carry a run loop; only
// localize_with_ollama.py remains there, and it only translates). Two
// implementations of one contract is a drift hazard: the scripts fell out of
// step with the client silently, and one of them shipped a broken success count
// while its renders were already published. Here the SUBMISSION side is the
// shared internal/batch expansion (same job ids and idempotency keys as
// cmd/batch-submit), the TRANSPORT is queue/client, and the manifest is data.
//
// The manifest is the same renderinggen.batch-manifest.v1 document
// cmd/batch-submit consumes. This package additionally reads the optional
// per-job reporting metadata (family, entity, attribution, entrance window) that
// the report and the corpus docs are built from; the submission decoder ignores
// it, so one file stays the single input of record.
package overlaybatch

import (
	"encoding/json"
	"fmt"
	"strings"
)

// reportProbe is this package's view of one manifest job: everything the report
// needs that the submission contract does not carry.
type reportProbe struct {
	ID     string `json:"id"`
	Family string `json:"family,omitempty"`
	Text   string `json:"text,omitempty"`
	// MotionID/PresetID are duplicated from the plan when the producer records
	// them at job level (the phrase/image corpora do).
	MotionID string `json:"motion_id,omitempty"`
	PresetID string `json:"preset_id,omitempty"`
	// Entity* and Asset* describe the image corpora's provenance.
	EntityID    string `json:"entity_id,omitempty"`
	EntityName  string `json:"entity_name,omitempty"`
	Asset       string `json:"asset,omitempty"`
	AssetSource string `json:"asset_source,omitempty"`
	Attribution string `json:"asset_attribution,omitempty"`
	// EntranceDuration* record the reveal window a preset corpus was rendered
	// with, so the report states the motion duration instead of implying it.
	EntranceDurationFrames  *int            `json:"entrance_duration_frames,omitempty"`
	EntranceDurationSeconds *float64        `json:"entrance_duration_seconds,omitempty"`
	RenderPlan              json.RawMessage `json:"render_plan"`
}

// manifestProbe is the envelope: batch id plus the jobs' reporting metadata.
type manifestProbe struct {
	SchemaVersion string        `json:"schema_version"`
	BatchID       string        `json:"batch_id"`
	Jobs          []reportProbe `json:"jobs"`
}

// decodeProbe reads the reporting metadata. It is deliberately permissive about
// the submission fields (internal/batch owns their validation) but strict about
// what the report claims: a job without an id cannot be reported.
func decodeProbe(raw []byte) (*manifestProbe, error) {
	var probe manifestProbe
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("overlaybatch: decode manifest metadata: %w", err)
	}
	if strings.TrimSpace(probe.BatchID) == "" {
		return nil, fmt.Errorf("overlaybatch: manifest carries no batch_id")
	}
	for _, job := range probe.Jobs {
		if strings.TrimSpace(job.ID) == "" {
			return nil, fmt.Errorf("overlaybatch: manifest %q carries a job with an empty id", probe.BatchID)
		}
	}
	return &probe, nil
}

// planProbe is the subset of a semantic plan the report recovers. The plan is
// authoritative for language/template/preset/text: a job-level copy could drift
// from what was actually rendered.
type planProbe struct {
	Language string `json:"language"`
	Items    []struct {
		Kind       string `json:"kind"`
		TemplateID string `json:"template_id"`
		PresetID   string `json:"preset_id"`
		MotionID   string `json:"motion_id"`
		Text       string `json:"text"`
		EntityID   string `json:"entity_id"`
	} `json:"items"`
}

// jobFacts are the report-relevant facts of one manifest job.
type jobFacts struct {
	family      string
	language    string
	template    string
	preset      string
	motion      string
	text        string
	entityID    string
	entityName  string
	asset       string
	assetSource string
	attribution string
	entranceF   *int
	entranceS   *float64
}

// facts merges the job-level metadata with the plan's own facts. Plan values win
// where both exist, because the plan is what the renderer consumed.
func (p reportProbe) facts() jobFacts {
	f := jobFacts{
		family:      strings.TrimSpace(p.Family),
		preset:      p.PresetID,
		motion:      p.MotionID,
		text:        p.Text,
		entityID:    p.EntityID,
		entityName:  p.EntityName,
		asset:       p.Asset,
		assetSource: p.AssetSource,
		attribution: p.Attribution,
		entranceF:   p.EntranceDurationFrames,
		entranceS:   p.EntranceDurationSeconds,
	}
	var plan planProbe
	if len(p.RenderPlan) > 0 && json.Unmarshal(p.RenderPlan, &plan) == nil && len(plan.Items) > 0 {
		item := plan.Items[0]
		if plan.Language != "" {
			f.language = plan.Language
		}
		if item.TemplateID != "" {
			f.template = item.TemplateID
		}
		if item.PresetID != "" {
			f.preset = item.PresetID
		}
		if item.MotionID != "" {
			f.motion = item.MotionID
		}
		if item.Text != "" {
			f.text = item.Text
		}
		if item.EntityID != "" {
			f.entityID = item.EntityID
		}
		if f.family == "" {
			f.family = familyOf(item.Kind, item.TemplateID)
		}
	}
	return f
}

// familyOf derives the report family from the item's kind/template when the
// manifest does not name one. It is a reporting label only: nothing in the
// render path branches on it.
func familyOf(kind, template string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "entity_image":
		return "image"
	case "important_phrase":
		return "phrase"
	}
	if strings.EqualFold(strings.TrimSpace(template), "IMAGE_OVERLAY") {
		return "image"
	}
	return "phrase"
}

// downloadFolder maps a family to the subdirectory its artifacts are collected
// in, so a corpus keeps the layout its README documents.
func downloadFolder(family string) string {
	switch family {
	case "phrase":
		return "phrases"
	case "image":
		return "images"
	default:
		return family
	}
}
