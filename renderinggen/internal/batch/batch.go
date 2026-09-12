// Package batch owns the batch submission contract: one master manifest is
// expanded deterministically into N queue jobs whose IDs and idempotency keys
// are content-derived, so re-submitting the same manifest (crash recovery,
// operator retry, pipeline replay) is a no-op instead of a double render.
//
// Two shapes are accepted:
//
//   - flat: the manifest carries the jobs directly (one entry per video);
//   - multilingual: the manifest carries one base plan + per-language overlay
//     plans (Strategy A: the base video is rendered once, then each language
//     only re-renders the overlay/text layers on top of the base artifact).
//
// The package is pure: no I/O, no clock, no randomness. The queue client is
// injected by the caller (cmd/batch-submit) so tests can use httptest.
package batch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// SchemaBatchManifestV1 identifies the batch manifest envelope.
const SchemaBatchManifestV1 = "renderinggen.batch-manifest.v1"

// SchemaMultilingualBatchV1 identifies the multilingual batch manifest
// (base render + per-language overlay renders).
const SchemaMultilingualBatchV1 = "renderinggen.batch-multilingual.v1"

// BatchID is the caller-chosen batch identifier. It scopes every derived
// job ID and idempotency key: two different batches can never collide even
// when they carry identical content.
type BatchID string

// FlatJob is one video inside a flat manifest.
type FlatJob struct {
	// ID is the logical per-batch job key (e.g. "video-001"). Combined with
	// BatchID it forms the queue job ID.
	ID string `json:"id"`
	// RenderPlan is the semantic overlay-plan.v1 (or concrete plan) payload
	// submitted verbatim as the job's render_plan.
	RenderPlan json.RawMessage `json:"render_plan"`
	// Assets are the content-addressed assets the plan references.
	Assets []queue.AssetRef `json:"assets,omitempty"`
}

// Manifest is the flat batch master: N videos, each with its own plan.
type Manifest struct {
	Schema  string    `json:"schema_version"`
	BatchID BatchID   `json:"batch_id"`
	Jobs    []FlatJob `json:"jobs"`
}

// LanguageJob is one language variant of a multilingual batch: it references
// the base render by job ID and carries only the language-specific overlay
// plan (text strings and timings differ; entities and coordinates are shared).
type LanguageJob struct {
	// Language is the BCP-47 tag (e.g. "en", "it", "es") used in derived IDs.
	Language string `json:"language"`
	// BaseJobID is the derived ID of the base render this overlay pass binds
	// to. It must equal the base entry's logical ID in the same manifest.
	BaseJobID string `json:"base_job_id"`
	// RenderPlan is the language-specific semantic overlay-plan.v1.
	RenderPlan json.RawMessage `json:"render_plan"`
	// Assets are the language-specific assets (localized ASS tracks, fonts,
	// audio) on top of whatever the base entry already carries.
	Assets []queue.AssetRef `json:"assets,omitempty"`
	// AudioSourceAsset is the localized voiceover muxed onto the base video
	// during the overlay pass. The field is part of the published contract
	// (contracts/renderinggen.batch-multilingual.v1.schema.json), so dropping it
	// silently meant a manifest that validated against the schema lost its
	// localized audio with no error anywhere: the asset was neither submitted
	// nor materialized, and the language job rendered with the base audio. It is
	// therefore folded into the job's Assets (the plan binds to it by hash) and
	// validated like every other asset.
	AudioSourceAsset *queue.AssetRef `json:"audio_source_asset,omitempty"`
}

// MultilingualManifest is Strategy A's batch master: one base video rendered
// once, then one overlay pass per language. The expansion keeps the base job
// as a parent (queue ParentJobID) so the per-language jobs remain traceable
// to the shared GPU work they reuse.
type MultilingualManifest struct {
	Schema  string        `json:"schema_version"`
	BatchID BatchID       `json:"batch_id"`
	Base    FlatJob       `json:"base"`
	Langs   []LanguageJob `json:"languages"`
}

// Result reports what one expansion produced. Submitted and Existing are
// disjoint: a replay of the same manifest lands everything in Existing and
// submits nothing.
type Result struct {
	BatchID   BatchID
	Submitted []string // job IDs sent to the queue (HTTP 201)
	Existing  []string // job IDs already present (HTTP 409 → idempotent no-op)
}

// --- deterministic identity -------------------------------------------------

// jobID derives the queue job ID: "<batch>:<logical>".
func jobID(b BatchID, logical string) string {
	return string(b) + ":" + logical
}

// idempotencyKey derives the stable idempotency key for one job. The key is
// a SHA-256 over (batch, logical id, plan bytes, asset refs) so an edited
// plan yields a NEW key (new job) while a byte-identical replay yields the
// SAME key (the queue's unique index resolves it to the existing job).
func idempotencyKey(b BatchID, logical string, plan json.RawMessage, assets []queue.AssetRef) string {
	h := sha256.New()
	fmt.Fprintf(h, "batch:%s\njob:%s\n", b, logical)
	h.Write(plan)
	// Asset refs are hashed in a canonical order so manifest formatting
	// cannot change the key.
	sorted := make([]queue.AssetRef, len(assets))
	copy(sorted, assets)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Hash != sorted[j].Hash {
			return sorted[i].Hash < sorted[j].Hash
		}
		return sorted[i].LogicalPath < sorted[j].LogicalPath
	})
	for _, a := range sorted {
		fmt.Fprintf(h, "asset:%s:%s:%s\n", a.Hash, a.LogicalPath, a.SourceURL)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- validation --------------------------------------------------------------

// validatePlanShape fails closed on plans that cannot possibly render:
// the payload must at least be a JSON object. Deep validation is the
// worker's job (overlay.CompileSemantic); the batch layer only refuses
// structurally impossible submissions.
func validatePlanShape(logical string, plan json.RawMessage) error {
	if len(plan) == 0 {
		return fmt.Errorf("batch: job %q has an empty render_plan", logical)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(plan, &probe); err != nil {
		return fmt.Errorf("batch: job %q render_plan is not a JSON object: %w", logical, err)
	}
	return nil
}

func validateAssets(logical string, assets []queue.AssetRef) error {
	seen := make(map[string]bool, len(assets))
	for _, a := range assets {
		if strings.TrimSpace(a.Hash) == "" || strings.TrimSpace(a.LogicalPath) == "" {
			return fmt.Errorf("batch: job %q carries an asset with empty hash or logical_path", logical)
		}
		if seen[a.LogicalPath] {
			return fmt.Errorf("batch: job %q carries duplicate asset logical_path %q", logical, a.LogicalPath)
		}
		seen[a.LogicalPath] = true
	}
	return nil
}

// Expand validates the flat manifest and returns the derived queue jobs.
func Expand(m Manifest) ([]queue.Job, error) {
	if m.Schema != SchemaBatchManifestV1 {
		return nil, fmt.Errorf("batch: unsupported manifest schema %q (want %q)", m.Schema, SchemaBatchManifestV1)
	}
	if strings.TrimSpace(string(m.BatchID)) == "" {
		return nil, fmt.Errorf("batch: batch_id is required")
	}
	if len(m.Jobs) == 0 {
		return nil, fmt.Errorf("batch: manifest %q carries no jobs", m.BatchID)
	}
	seen := make(map[string]bool, len(m.Jobs))
	jobs := make([]queue.Job, 0, len(m.Jobs))
	for _, j := range m.Jobs {
		if strings.TrimSpace(j.ID) == "" {
			return nil, fmt.Errorf("batch: manifest %q carries a job with an empty id", m.BatchID)
		}
		if seen[j.ID] {
			return nil, fmt.Errorf("batch: manifest %q carries duplicate job id %q", m.BatchID, j.ID)
		}
		seen[j.ID] = true
		if err := validatePlanShape(j.ID, j.RenderPlan); err != nil {
			return nil, err
		}
		if err := validateAssets(j.ID, j.Assets); err != nil {
			return nil, err
		}
		jobs = append(jobs, queue.Job{
			ID:             jobID(m.BatchID, j.ID),
			Schema:         queue.JobSchemaV1,
			Version:        queue.JobSchemaVersionV1,
			IdempotencyKey: idempotencyKey(m.BatchID, j.ID, j.RenderPlan, j.Assets),
			RenderPlan:     j.RenderPlan,
			Assets:         j.Assets,
		})
	}
	return jobs, nil
}

// ExpandMultilingual validates the Strategy-A manifest and returns
// 1 base job + N per-language overlay jobs, in submission order (base first).
//
// The base job carries the full base plan (background/source video, shared
// entities) and NO language text; each language job carries the localized
// overlay plan and declares the base as parent. Language jobs are ordered by
// the manifest's language list; duplicate languages are rejected.
func ExpandMultilingual(m MultilingualManifest) ([]queue.Job, error) {
	if m.Schema != SchemaMultilingualBatchV1 {
		return nil, fmt.Errorf("batch: unsupported manifest schema %q (want %q)", m.Schema, SchemaMultilingualBatchV1)
	}
	if strings.TrimSpace(string(m.BatchID)) == "" {
		return nil, fmt.Errorf("batch: batch_id is required")
	}
	base := m.Base
	if strings.TrimSpace(base.ID) == "" {
		return nil, fmt.Errorf("batch: manifest %q base entry has an empty id", m.BatchID)
	}
	if err := validatePlanShape(base.ID, base.RenderPlan); err != nil {
		return nil, err
	}
	if err := validateAssets(base.ID, base.Assets); err != nil {
		return nil, err
	}
	seenLang := make(map[string]bool, len(m.Langs))
	jobs := make([]queue.Job, 0, 1+len(m.Langs))
	baseQueueID := jobID(m.BatchID, base.ID)
	jobs = append(jobs, queue.Job{
		ID:             baseQueueID,
		Schema:         queue.JobSchemaV1,
		Version:        queue.JobSchemaVersionV1,
		IdempotencyKey: idempotencyKey(m.BatchID, base.ID, base.RenderPlan, base.Assets),
		RenderPlan:     base.RenderPlan,
		Assets:         base.Assets,
	})
	for _, l := range m.Langs {
		lang := strings.TrimSpace(l.Language)
		if lang == "" {
			return nil, fmt.Errorf("batch: manifest %q carries a language entry with an empty language", m.BatchID)
		}
		if seenLang[lang] {
			return nil, fmt.Errorf("batch: manifest %q carries duplicate language %q", m.BatchID, lang)
		}
		seenLang[lang] = true
		logical := base.ID + ".overlay." + lang
		if strings.TrimSpace(l.BaseJobID) != "" && l.BaseJobID != base.ID {
			return nil, fmt.Errorf("batch: language %q declares base_job_id %q but the manifest base is %q", lang, l.BaseJobID, base.ID)
		}
		if err := validatePlanShape(logical, l.RenderPlan); err != nil {
			return nil, err
		}
		assets, err := languageAssets(logical, l)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, queue.Job{
			ID:             jobID(m.BatchID, logical),
			Schema:         queue.JobSchemaV1,
			Version:        queue.JobSchemaVersionV1,
			JobType:        "overlay.render",
			ParentJobID:    baseQueueID,
			IdempotencyKey: idempotencyKey(m.BatchID, logical, l.RenderPlan, assets),
			RenderPlan:     l.RenderPlan,
			Assets:         assets,
		})
	}
	return jobs, nil
}

// languageAssets is the single place that builds a language job's asset list:
// the declared language assets plus the localized voiceover, de-duplicated by
// logical path. The voiceover is validated with the same rules as every other
// asset (non-empty hash/logical_path, no duplicate path), so an unimplementable
// declaration fails closed instead of being dropped.
func languageAssets(logical string, l LanguageJob) ([]queue.AssetRef, error) {
	assets := make([]queue.AssetRef, 0, len(l.Assets)+1)
	assets = append(assets, l.Assets...)
	if voice := l.AudioSourceAsset; voice != nil {
		if strings.TrimSpace(voice.Hash) == "" || strings.TrimSpace(voice.LogicalPath) == "" {
			return nil, fmt.Errorf("batch: job %q audio_source_asset requires hash and logical_path", logical)
		}
		replaced := false
		for i, a := range assets {
			if a.LogicalPath != voice.LogicalPath {
				continue
			}
			if a.Hash != voice.Hash {
				return nil, fmt.Errorf("batch: job %q declares logical_path %q twice with different hashes", logical, voice.LogicalPath)
			}
			assets[i] = *voice
			replaced = true
			break
		}
		if !replaced {
			assets = append(assets, *voice)
		}
	}
	if err := validateAssets(logical, assets); err != nil {
		return nil, err
	}
	return assets, nil
}

// Decode detects the manifest shape (flat vs multilingual) from its schema
// field and expands accordingly.
func Decode(raw []byte) ([]queue.Job, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("batch: decode manifest: %w", err)
	}
	switch probe.SchemaVersion {
	case SchemaBatchManifestV1:
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("batch: decode flat manifest: %w", err)
		}
		return Expand(m)
	case SchemaMultilingualBatchV1:
		var m MultilingualManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("batch: decode multilingual manifest: %w", err)
		}
		return ExpandMultilingual(m)
	case "":
		return nil, fmt.Errorf("batch: manifest carries no schema_version")
	default:
		return nil, fmt.Errorf("batch: unsupported manifest schema %q", probe.SchemaVersion)
	}
}
