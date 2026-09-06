package processor

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
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

// TestValidateAllowsLegacyEnvelopeForSymbolicKeys pins the legacy allowance:
// a development fixture carrying a symbolic (non-SHA-256) asset key predates
// the v1 envelope and may omit schema/version — the same allowance the asset
// resolvers use to waive content verification. Strictening the envelope must
// never reject these fixtures (see the audit's gating requirement).
func TestValidateAllowsLegacyEnvelopeForSymbolicKeys(t *testing.T) {
	job := &queue.Job{
		ID:         "legacy-fixture",
		RenderPlan: json.RawMessage(`{"schema":"chronon.render-plan.v2","canvas":{"width":1280,"height":720}}`),
		Assets:     []queue.AssetRef{{Hash: "abc", LogicalPath: "videos/base.mp4"}},
	}
	if err := validate(job); err != nil {
		t.Fatalf("legacy fixture with symbolic key and no envelope rejected: %v", err)
	}
	// A legacy fixture with a declared envelope still validates.
	job.Schema = queue.JobSchemaV1
	job.Version = queue.JobSchemaVersionV1
	if err := validate(job); err != nil {
		t.Fatalf("legacy fixture with envelope rejected: %v", err)
	}
}
