package model

// Artifact is the metadata of a rendered artifact, produced by a worker on job
// completion and persisted to render_artifacts. It carries the copy-only
// certification (codec, profile, GOP/keyframe flags) that VeloxEditing relies
// on to assemble an overlay without re-decoding or re-encoding it.
type Artifact struct {
	ID                 string `json:"id,omitempty"`
	Kind               string `json:"kind,omitempty"`
	StorageKey         string `json:"storage_key,omitempty"`
	URL                string `json:"url,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	MimeType           string `json:"mime_type,omitempty"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	FPSNum             int    `json:"fps_num,omitempty"`
	FPSDen             int    `json:"fps_den,omitempty"`
	FrameCount         int    `json:"frame_count,omitempty"`
	DurationUS         int64  `json:"duration_us,omitempty"`
	ProfileID          string `json:"profile_id,omitempty"`
	CopyEligible       bool   `json:"copy_eligible,omitempty"`
	Codec              string `json:"codec,omitempty"`
	CodecProfile       string `json:"codec_profile,omitempty"`
	ClosedGOP          bool   `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool   `json:"first_frame_keyframe,omitempty"`
}
