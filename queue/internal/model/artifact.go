package model

import "encoding/json"

// Artifact is the metadata of a rendered artifact, produced by a worker on job
// completion and persisted to render_artifacts. It carries the copy-only
// certification (codec, profile, GOP/keyframe flags) that VeloxEditing relies
// on to assemble an overlay without re-decoding or re-encoding it.
type Artifact struct {
	ID           string `json:"id,omitempty"`
	Kind         string `json:"kind,omitempty"`
	StorageKey   string `json:"storage_key,omitempty"`
	ArtifactURL  string `json:"artifact_url,omitempty"`
	ArtifactHash string `json:"artifact_hash,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	FPSNum       int    `json:"fps_num,omitempty"`
	FPSDen       int    `json:"fps_den,omitempty"`
	FrameCount   int    `json:"frame_count,omitempty"`
	DurationUS   int64  `json:"duration_us,omitempty"`
	ProfileID    string `json:"profile_id,omitempty"`
	CopyEligible bool   `json:"copy_eligible,omitempty"`
	Codec        string `json:"codec,omitempty"`
	CodecProfile string `json:"codec_profile,omitempty"`
	// ClosedGOP certifies a uniform closed-GOP structure (leading keyframe +
	// strictly periodic GOP boundaries), certified by the worker's media probe
	// from the container sync-sample table. It is NOT first_frame_keyframe:
	// a segment may start cleanly without having closed, regular GOPs.
	ClosedGOP          bool               `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool               `json:"first_frame_keyframe,omitempty"`
	Backend            string             `json:"backend,omitempty"`
	ChrononVersion     string             `json:"chronon_version,omitempty"`
	Metrics            map[string]float64 `json:"metrics,omitempty"`
	// ChrononTelemetry is the BOUNDED telemetry summary document
	// (`<output>.telemetry-summary.json`, schema
	// chronon3d.render-telemetry-summary.v1) — the only Chronon telemetry
	// surface the worker ingests (observability ownership, Phase 10).
	// PostgreSQL stores it as JSONB; Metrics remains the documented numeric
	// projection for clients. It never contains per-frame arrays.
	ChrononTelemetry json.RawMessage `json:"chronon_telemetry,omitempty"`
	// ChrononTiming* are the RawTimingArtifactRef: the RAW deep-profile
	// timing sidecar (`<output>.timing.json`, including the unbounded
	// per-frame frame_times_ms array) is preserved as an OPAQUE artifact in
	// the object store under its content address — bytes → hash → store →
	// reference. The worker never parses or mutates it; these fields keep it
	// fetchable for post-mortem without ever inlining the array. Absent when
	// the sidecar file did not exist or could not be preserved (fail-open).
	ChrononTimingStorageKey  string `json:"chronon_timing_storage_key,omitempty"`
	ChrononTimingURL         string `json:"chronon_timing_url,omitempty"`
	ChrononTimingSHA256      string `json:"chronon_timing_sha256,omitempty"`
	ChrononTimingSizeBytes   int64  `json:"chronon_timing_size_bytes,omitempty"`
	ChrononTimingContentType string `json:"chronon_timing_content_type,omitempty"`

	// DriveFileID and DriveLink record the external Google Drive publication
	// of the artifact. They are populated only after the Drive upload succeeds;
	// a job stuck in StateRendered has a rendered artifact without them.
	DriveFileID  string `json:"drive_file_id,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	Container    string `json:"container,omitempty"`
	PixelFormat  string `json:"pixel_format,omitempty"`
	AudioStreams int    `json:"audio_streams,omitempty"`
}
