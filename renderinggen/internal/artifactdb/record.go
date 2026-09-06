// Package artifactdb is an optional worker-local artifact mirror. Central
// PostgreSQL is the production source of truth; this database is diagnostic.
// The record carries the render provenance, the probed media facts and the
// plan section "DB metrics" (overlay counts, preset, per-phase microseconds,
// input/output bytes). The recorder is an interface so the worker stays
// engine-agnostic; the SQLite implementation is pure Go and therefore valid
// under the CGO_ENABLED=0 worker build.
package artifactdb

import (
	"encoding/json"
	"time"
)

// ArtifactRecord is a local diagnostic snapshot of one rendered artifact.
type ArtifactRecord struct {
	JobID string
	// ArtifactHash is the SHA-256 of the rendered bytes; StorageKey is the
	// object-store key they were published under. The pipeline invariant is
	// local_sha == objectstore_sha == db_sha (plan section "Drive").
	ArtifactHash   string
	StorageKey     string
	SizeBytes      int64 // output bytes, == len(rendered mp4)
	ContentType    string
	Backend        string
	ChrononVersion string
	ProfileID      string

	// Probe facts (from ffprobe, never from the plan).
	Container          string
	Codec              string
	CodecProfile       string
	PixelFormat        string
	Width              int
	Height             int
	FPSNum             int
	FPSDen             int
	FrameCount         int
	DurationUS         int64
	AudioStreams       int
	FirstFrameKeyframe bool

	// Semantic counters from the compiled overlay plan (zero for legacy).
	EntityCount        int
	ImportantPhraseCnt int
	ImportantWordCnt   int
	ImageCount         int
	LightLeakCount     int
	PresetID           string

	// Per-phase wall-clock microseconds (plan section "DB metrics"). The
	// encoder runs inside Chronon's render phase (chronon_render_us), so
	// there is no separately measured encode phase on the worker and no
	// always-zero encode_us column is projected.
	OverlayCompileUS    int64
	AssetMaterializeUS  int64
	ChrononRenderUS     int64
	SHA256US            int64
	ObjectStoreUploadUS int64
	DriveUploadUS       int64
	TotalUS             int64

	// ChrononTelemetry is the job-level telemetry document ingested from
	// Chronon's timing sidecar. Chronon is the source of truth for
	// plan/graph/GPU/encoder timing, so this is stored verbatim as JSON
	// (Chronon owns the schema); the worker records only its distributive
	// phases in the typed columns above. nil when the sidecar was missing.
	ChrononTelemetry json.RawMessage

	// Bytes in and out of the job.
	InputBytes  int64
	OutputBytes int64

	CreatedAt time.Time
}

// Metrics returns the plan's "DB metrics" projection as a stable map, used by
// reports and tests to assert the recorded numbers without coupling to the
// record layout.
func (r ArtifactRecord) Metrics() map[string]float64 {
	return map[string]float64{
		"entity_count":           float64(r.EntityCount),
		"important_phrase_count": float64(r.ImportantPhraseCnt),
		"important_word_count":   float64(r.ImportantWordCnt),
		"image_count":            float64(r.ImageCount),
		"light_leak_count":       float64(r.LightLeakCount),
		"overlay_compile_us":     float64(r.OverlayCompileUS),
		"asset_materialize_us":   float64(r.AssetMaterializeUS),
		"chronon_render_us":      float64(r.ChrononRenderUS),
		"sha256_us":              float64(r.SHA256US),
		"objectstore_upload_us":  float64(r.ObjectStoreUploadUS),
		"drive_upload_us":        float64(r.DriveUploadUS),
		"total_us":               float64(r.TotalUS),
		"input_bytes":            float64(r.InputBytes),
		"output_bytes":           float64(r.OutputBytes),
		"frame_count":            float64(r.FrameCount),
		"duration_us":            float64(r.DurationUS),
		"width":                  float64(r.Width),
		"height":                 float64(r.Height),
		"fps":                    float64(r.FPSNum) / float64(r.FPSDen),
	}
}
