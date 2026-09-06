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
	// ChrononTelemetry is the raw job-level timing document. PostgreSQL stores
	// it as JSONB; Metrics remains the bounded numeric projection for clients.
	ChrononTelemetry json.RawMessage `json:"chronon_telemetry,omitempty"`

	// DriveFileID and DriveLink record the external Google Drive publication
	// of the artifact. They are populated only after the Drive upload succeeds;
	// a job stuck in StateRendered has a rendered artifact without them.
	DriveFileID  string `json:"drive_file_id,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	Container    string `json:"container,omitempty"`
	PixelFormat  string `json:"pixel_format,omitempty"`
	AudioStreams int    `json:"audio_streams,omitempty"`
}
