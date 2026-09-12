// Package metricnames owns the worker's metric-name vocabulary and the unit
// each name carries.
//
// Before this package the vocabulary existed only as string literals spread
// across six carriers — the staged pipeline, the GPU stage, the store phase,
// the artifact-ledger projection, the SQLite mirror's column map, the queue's
// processing_metrics rows (whose unit was re-derived by parsing the name
// suffix) — with no single place that could answer "which metrics exist and in
// which unit?". A phase rename therefore had to be applied in every carrier by
// hand, a typo was persisted as an untyped counter, and obsolete names stayed
// quoted in documentation.
//
// It is a leaf package (no RenderingGen imports) so BOTH the processor and the
// artifact mirror can import it without an import cycle. The unit is carried by
// the name (the `_us`/`_ms` suffix is the wire convention the queue relies on);
// Unit() states it once instead of every consumer re-parsing the suffix.
package metricnames

import "strings"

// Phase timings, in microseconds, of the staged render pipeline.
const (
	OverlayCompileUS    = "overlay_compile_us"
	AssetMaterializeUS  = "asset_materialize_us"
	PlanUS              = "plan_us"
	SubtitleBurnUS      = "subtitle_burn_us"
	ChrononRenderUS     = "chronon_render_us"
	RenderUS            = "render_us" // alias of ChrononRenderUS on the artifact metrics map
	ProbeUS             = "probe_us"
	SHA256US            = "sha256_us"
	ObjectStoreUploadUS = "objectstore_upload_us"
	PublishUS           = "publish_us"
	DriveUploadUS       = "drive_upload_us"
	TotalUS             = "total_us"
	GPUGapUS            = "gpu_gap_us"
)

// The same phases in milliseconds. The pipeline records both spellings for the
// benchmark/test surface; the ledger consumes the microsecond form.
const (
	OverlayCompileMS    = "overlay_compile_ms"
	AssetMaterializeMS  = "asset_materialize_ms"
	PlanMS              = "plan_ms"
	SubtitleBurnMS      = "subtitle_burn_ms"
	RenderMS            = "render_ms"
	ProbeMS             = "probe_ms"
	SHA256MS            = "sha256_ms"
	ObjectStoreUploadMS = "objectstore_upload_ms"
	PublishMS           = "publish_ms"
	DrivePublishMS      = "drive_publish_ms"
	TotalMS             = "total_ms"
)

// Phase STEMS. The staged pipeline records both spellings of a phase from a
// single stem (`metrics[phase+"_us"]`, `metrics[phase+"_ms"]`), so a stem is
// only valid when BOTH names are declared — StemHasPair is that check, and the
// processor vocabulary test walks every stem the pipeline passes around.
const (
	AssetMaterializeStem  = "asset_materialize"
	PlanStem              = "plan"
	RenderStem            = "render"
	PublishStem           = "publish"
	ProbeStem             = "probe"
	OverlayCompileStem    = "overlay_compile"
	SubtitleBurnStem      = "subtitle_burn"
	SHA256Stem            = "sha256"
	ObjectStoreUploadStem = "objectstore_upload"
)

// StemHasPair reports whether both `<stem>_us` and `<stem>_ms` are declared.
func StemHasPair(stem string) bool {
	_, hasUS := vocab[stem+"_us"]
	_, hasMS := vocab[stem+"_ms"]
	return hasUS && hasMS
}

// Counters and non-time facts.
const (
	SubtitleLayers                    = "subtitle_layers"
	RenderFramesDone                  = "render_frames_done"
	RenderFramesTotal                 = "render_frames_total"
	RenderFPS                         = "render_fps"
	ProfileStrippedByConfig           = "profile_stripped_by_config"
	AudioInertParams                  = "audio_inert_params"
	AudioModeUnsupported              = "audio_mode_unsupported"
	UnknownTemplates                  = "unknown_templates"
	ChrononGPUCompositionPipe         = "chronon_gpu_composition_pipe"
	MirrorFailure                     = "mirror_failure"
	DriveUploadChunks                 = "drive_upload_chunks"
	DriveUploadBytes                  = "drive_upload_bytes"
	ChrononTimingPreserved            = "chronon_timing_preserved"
	ChrononTimingBytes                = "chronon_timing_bytes"
	ClosedGOPUncertifiable            = "closed_gop_uncertifiable"
	PublicationDriveSkippedPolicy     = "publication_drive_skipped_by_policy"
	PublicationDriveSkippedCapability = "publication_drive_skipped_no_capability"

	// Degradation counters. The three phases below are fail-open BY DESIGN —
	// a missing telemetry sidecar or a workspace that could not be removed must
	// never fail an otherwise valid render. Fail-open without a counter is
	// indistinguishable from success, so each degradation is now emitted as a
	// metric instead of existing only as a log line.
	//
	// WorkspaceCleanupFailures is process-cumulative and reported on /health
	// (the jobs root is often tmpfs, so repeated cleanup failures consume RAM).
	// The two Chronon telemetry counters are per-artifact and land in the ledger.
	WorkspaceCleanupFailures    = "workspace_cleanup_failures"
	ChrononTelemetryMissing     = "chronon_telemetry_missing"
	ChrononTimingSidecarMissing = "chronon_timing_sidecar_missing"
)

// Ledger facts mirrored into the local artifact database (counts, bytes and
// media facts the mirror projects as numbers).
const (
	EntityCount          = "entity_count"
	ImportantPhraseCount = "important_phrase_count"
	ImportantWordCount   = "important_word_count"
	ImageCount           = "image_count"
	LightLeakCount       = "light_leak_count"
	InputBytes           = "input_bytes"
	OutputBytes          = "output_bytes"
	FrameCount           = "frame_count"
	DurationUS           = "duration_us"
	Width                = "width"
	Height               = "height"
	FPS                  = "fps"
)

// Chronon-owned telemetry is projected behind this prefix: the keys are the
// engine's (schema chronon3d.render-telemetry-summary.v1), the prefix is the
// worker's namespace for them on the artifact metrics map.
const (
	ChrononSummaryPrefix = "chronon_summary_"
	ChrononTimingPrefix  = "chronon_timing_"
)

// UnitUS/UnitMS/UnitCount/UnitBytes are the units a declared name can carry;
// the queue derives the same units from the name suffix, so the two must agree.
const (
	UnitUS    = "us"
	UnitMS    = "ms"
	UnitCount = "count"
	UnitBytes = "bytes"
)

// vocab maps every declared name to its unit. It is the single list the
// vocabulary test iterates.
var vocab = map[string]string{
	OverlayCompileUS: UnitUS, AssetMaterializeUS: UnitUS, PlanUS: UnitUS,
	SubtitleBurnUS: UnitUS, ChrononRenderUS: UnitUS, RenderUS: UnitUS,
	ProbeUS: UnitUS, SHA256US: UnitUS, ObjectStoreUploadUS: UnitUS,
	PublishUS: UnitUS, DriveUploadUS: UnitUS, TotalUS: UnitUS, GPUGapUS: UnitUS,

	OverlayCompileMS: UnitMS, AssetMaterializeMS: UnitMS, PlanMS: UnitMS,
	SubtitleBurnMS: UnitMS, RenderMS: UnitMS, ProbeMS: UnitMS, SHA256MS: UnitMS,
	ObjectStoreUploadMS: UnitMS, PublishMS: UnitMS, DrivePublishMS: UnitMS,
	TotalMS: UnitMS,

	SubtitleLayers: UnitCount, RenderFramesDone: UnitCount, RenderFramesTotal: UnitCount,
	RenderFPS: "fps", ProfileStrippedByConfig: UnitCount, AudioInertParams: UnitCount,
	AudioModeUnsupported: UnitCount, UnknownTemplates: UnitCount, ChrononGPUCompositionPipe: UnitCount,
	MirrorFailure:     UnitCount,
	DriveUploadChunks: UnitCount, DriveUploadBytes: UnitBytes,
	ChrononTimingPreserved: UnitCount, ChrononTimingBytes: UnitBytes,
	ClosedGOPUncertifiable:        UnitCount,
	PublicationDriveSkippedPolicy: UnitCount, PublicationDriveSkippedCapability: UnitCount,
	WorkspaceCleanupFailures: UnitCount, ChrononTelemetryMissing: UnitCount,
	ChrononTimingSidecarMissing: UnitCount,

	EntityCount: UnitCount, ImportantPhraseCount: UnitCount, ImportantWordCount: UnitCount,
	ImageCount: UnitCount, LightLeakCount: UnitCount, InputBytes: UnitBytes,
	OutputBytes: UnitBytes, FrameCount: UnitCount, DurationUS: UnitUS,
	Width: UnitCount, Height: UnitCount, FPS: "fps",
}

// All returns the declared vocabulary names (the map is not exported so callers
// cannot mutate the single list).
func All() []string {
	out := make([]string, 0, len(vocab))
	for name := range vocab {
		out = append(out, name)
	}
	return out
}

// Unit returns the declared unit for a name. The second result is false for an
// undeclared name; for a Chronon-owned name carrying one of the projection
// prefixes the unit is the engine's, so it is reported as recognized with an
// empty unit.
func Unit(name string) (string, bool) {
	// A worker-declared name always wins: `chronon_timing_preserved` and
	// `chronon_timing_bytes` are worker-owned counters even though they carry
	// the projection prefix.
	if unit, ok := vocab[name]; ok {
		return unit, true
	}
	if strings.HasPrefix(name, ChrononSummaryPrefix) || strings.HasPrefix(name, ChrononTimingPrefix) {
		return "", true // owner: Chronon (telemetry summary schema)
	}
	return "", false
}

// Declared reports whether a metric name belongs to the vocabulary (an
// undeclared name is a typo that the queue would persist as an untyped metric).
func Declared(name string) bool {
	_, ok := Unit(name)
	return ok
}
