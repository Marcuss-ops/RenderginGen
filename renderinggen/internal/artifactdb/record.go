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

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
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

	// ChrononTelemetry is the BOUNDED telemetry summary ingested from Chronon's
	// `<output>.telemetry-summary.json` (schema
	// chronon3d.render-telemetry-summary.v1) — the only Chronon telemetry
	// surface the worker consumes (observability ownership, Phase 10). It is
	// stored verbatim as JSON (Chronon owns the schema); the worker records
	// only its distributive phases in the typed columns above. nil when the
	// summary was missing.
	ChrononTelemetry json.RawMessage

	// ChrononTiming* mirror the RawTimingArtifactRef on the queue artifact:
	// the content-addressed object-store key/url/sha of the verbatim
	// `<output>.timing.json` RAW deep profile (including frame_times_ms),
	// preserved opaquely for post-mortem. Empty when not preserved.
	ChrononTimingStorageKey  string
	ChrononTimingURL         string
	ChrononTimingSHA256      string
	ChrononTimingSizeBytes   int64
	ChrononTimingContentType string

	// Bytes in and out of the job.
	InputBytes  int64
	OutputBytes int64

	CreatedAt time.Time
}

// Metrics returns the plan's "DB metrics" projection as a stable map, used by
// reports and tests to assert the recorded numbers without coupling to the
// record layout. The names are the worker's declared vocabulary
// (internal/metricnames), not local literals: the ledger row and the artifact
// metrics map must agree on every name, and a rename has one owner.
func (r ArtifactRecord) Metrics() map[string]float64 {
	return map[string]float64{
		metricnames.EntityCount:          float64(r.EntityCount),
		metricnames.ImportantPhraseCount: float64(r.ImportantPhraseCnt),
		metricnames.ImportantWordCount:   float64(r.ImportantWordCnt),
		metricnames.ImageCount:           float64(r.ImageCount),
		metricnames.LightLeakCount:       float64(r.LightLeakCount),
		metricnames.OverlayCompileUS:     float64(r.OverlayCompileUS),
		metricnames.AssetMaterializeUS:   float64(r.AssetMaterializeUS),
		metricnames.ChrononRenderUS:      float64(r.ChrononRenderUS),
		metricnames.SHA256US:             float64(r.SHA256US),
		metricnames.ObjectStoreUploadUS:  float64(r.ObjectStoreUploadUS),
		metricnames.DriveUploadUS:        float64(r.DriveUploadUS),
		metricnames.TotalUS:              float64(r.TotalUS),
		metricnames.InputBytes:           float64(r.InputBytes),
		metricnames.OutputBytes:          float64(r.OutputBytes),
		metricnames.FrameCount:           float64(r.FrameCount),
		metricnames.DurationUS:           float64(r.DurationUS),
		metricnames.Width:                float64(r.Width),
		metricnames.Height:               float64(r.Height),
		metricnames.FPS:                  float64(r.FPSNum) / float64(r.FPSDen),
	}
}
