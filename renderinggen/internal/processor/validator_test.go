package processor

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// TestValidateRequiresEnvelopeForContentAddressedJobs pins the ingress
// contract: a job whose assets are all canonical SHA-256 content addresses
// (the production contract) must declare the renderinggen.job.v1 envelope at
// claim time. Empty schema or zero version fail here — never later as a
// compile/render failure far from the producer bug.
func TestValidateRequiresEnvelopeForContentAddressedJobs(t *testing.T) {
	job := &queue.Job{
		ID:      "content-job",
		Schema:  queue.JobSchemaV1,
		Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
		  "schema_version":"renderinggen.overlay-plan.v1",
		  "plan_id":"p","video_id":"v",
		  "width":1280,"height":720,"fps_num":30,"fps_den":1,
		  "source":{"asset_id":"v","sha256":"79fd615a866fe7f9eb4da8d9c41ab57e3bd48056df42fd2c13e4d461a87afbe3"}
		}`),
		Assets: []queue.AssetRef{{Hash: "79fd615a866fe7f9eb4da8d9c41ab57e3bd48056df42fd2c13e4d461a87afbe3", LogicalPath: "videos/base.mp4"}},
	}
	if err := validate(job); err != nil {
		t.Fatalf("valid content-addressed job rejected: %v", err)
	}

	// Empty schema / zero version are producer bugs on this path, not legacy.
	noSchema := *job
	noSchema.Schema = ""
	if err := validate(&noSchema); err == nil {
		t.Fatal("content-addressed job with empty schema must be rejected")
	}
	noVersion := *job
	noVersion.Version = 0
	if err := validate(&noVersion); err == nil {
		t.Fatal("content-addressed job with zero version must be rejected")
	}
}

// TestValidateFrameRangeEnforcedAgainstPlanDuration pins the chunk contract
// at the prepare boundary: a half-open chunk that falls outside the plan's
// certified duration is a producer bug that must fail before any asset is
// downloaded, not inside Chronon after the GPU lane and the lease are spent.
func TestValidateFrameRangeEnforcedAgainstPlanDuration(t *testing.T) {
	plan := &overlay.Plan{Canvas: overlay.Canvas{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, DurationFrames: 150}}
	cases := []struct {
		name    string
		range_  *queue.FrameRange
		wantErr bool
	}{
		{name: "no frame range renders the whole plan", range_: nil},
		{name: "whole plan", range_: &queue.FrameRange{Start: 0, End: 150}},
		{name: "interior chunk", range_: &queue.FrameRange{Start: 60, End: 90}},
		{name: "single frame at zero", range_: &queue.FrameRange{Start: 0, End: 1}},
		{name: "past the end", range_: &queue.FrameRange{Start: 150, End: 180}, wantErr: true},
		{name: "straddles the end", range_: &queue.FrameRange{Start: 120, End: 151}, wantErr: true},
		{name: "inverted", range_: &queue.FrameRange{Start: 90, End: 90}, wantErr: true},
		{name: "negative start", range_: &queue.FrameRange{Start: -1, End: 10}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := &queue.Job{ID: "chunk", FrameRange: tc.range_}
			err := validateFrameRange(job, plan)
			if tc.wantErr && err == nil {
				t.Fatalf("validateFrameRange(%+v) = nil, want an error", tc.range_)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateFrameRange(%+v) = %v, want nil", tc.range_, err)
			}
		})
	}

	// An uncertified plan duration must not invent a bound it cannot prove;
	// the compiler already rejects a zero-duration semantic plan.
	if err := validateFrameRange(&queue.Job{ID: "chunk", FrameRange: &queue.FrameRange{Start: 0, End: 999}}, &overlay.Plan{}); err != nil {
		t.Fatalf("uncertified plan duration must not fail the range check: %v", err)
	}
}

// TestValidateAllowsLegacyEnvelopeForSymbolicKeys pins the legacy allowance:
// a fixture that EXPLICITLY declares job_type "legacy" and carries a symbolic
// (non-SHA-256) asset key predates the v1 envelope and may omit schema/version
// — the same allowance the asset resolvers use to waive content verification.
func TestValidateAllowsLegacyEnvelopeForSymbolicKeys(t *testing.T) {
	job := &queue.Job{
		ID:         "legacy-fixture",
		JobType:    queue.JobTypeLegacy,
		RenderPlan: json.RawMessage(`{"schema":"chronon.render-plan.v2","canvas":{"width":1280,"height":720}}`),
		Assets:     []queue.AssetRef{{Hash: "abc", LogicalPath: "videos/base.mp4"}},
	}
	if err := validate(job); err != nil {
		t.Fatalf("legacy fixture with symbolic key and no envelope rejected: %v", err)
	}

	// The explicit marker is the ONLY legacy signal. The byte-identical job
	// without it must fail here: a dropped schema plus a shortened asset hash
	// used to disable the whole envelope check and the content verification
	// that keys off it, letting an unvalidated payload reach Chronon.
	unmarked := *job
	unmarked.JobType = ""
	if err := validate(&unmarked); err == nil {
		t.Fatal("symbolic asset key without the explicit legacy marker must be rejected")
	}

	// A legacy fixture with a declared envelope still validates.
	job.Schema = queue.JobSchemaV1
	job.Version = queue.JobSchemaVersionV1
	if err := validate(job); err != nil {
		t.Fatalf("legacy fixture with envelope rejected: %v", err)
	}
}
