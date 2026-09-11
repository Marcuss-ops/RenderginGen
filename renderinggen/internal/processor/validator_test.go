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

// TestValidateRejectsEveryJobWithoutTheV1Envelope pins that there is NO
// legacy opt-out left. The historical "job_type: legacy" allowance skipped the
// envelope check but could never render — PrepareJob still compiles through
// the semantic compiler, which accepts only renderinggen.overlay-plan.v1 — so
// it only relocated the failure. A symbolic (non-SHA-256) asset key is not a
// reason to accept an unvalidated envelope: the v1 schema/version are required
// on every path.
func TestValidateRejectsEveryJobWithoutTheV1Envelope(t *testing.T) {
	base := &queue.Job{
		ID:         "legacy-fixture",
		JobType:    "legacy",
		RenderPlan: json.RawMessage(`{"schema":"chronon.render-plan.v2","canvas":{"width":1280,"height":720}}`),
		Assets:     []queue.AssetRef{{Hash: "abc", LogicalPath: "videos/base.mp4"}},
	}
	if err := validate(base); err == nil {
		t.Fatal("a job without the v1 envelope must be rejected even when it is marked legacy")
	}

	// A symbolic key is equally rejected: content-addressed verification is a
	// requirement of the production path, not of the job-author's assertion.
	withEnvelope := *base
	withEnvelope.Schema = queue.JobSchemaV1
	withEnvelope.Version = queue.JobSchemaVersionV1
	withEnvelope.JobType = ""
	if err := validate(&withEnvelope); err != nil {
		t.Fatalf("job with the declared v1 envelope rejected: %v", err)
	}
}
