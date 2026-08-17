package processor

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// TestProcessEndToEndCLI runs the full pipeline against the real chronon3d_cli
// binary (skipped when it is not installed):
//
//	job -> validate -> workspace -> materialize -> plan.json -> real render
//	-> result.mp4 -> sha256 -> artifact store -> artifact metadata
//
// The job uses the asset-free color smoke plan, so the flow is fully
// self-contained and proves the CLI wiring end to end without sample media.
func TestProcessEndToEndCLI(t *testing.T) {
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	cli := &chronon.Client{Home: home}
	if err := cli.Verify(); err != nil {
		t.Skipf("chronon3d_cli not available: %v", err)
	}

	store := storage.New(storage.NewMemory(), storage.Options{})
	proc := New(t.TempDir(), "software", cli.Version(), "http://store:9000", store, cli)

	job := &queue.Job{
		ID:         "e2e-cli-smoke",
		Schema:     queue.JobSchemaV1,
		Version:    queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(chronon.ExampleColorSmokePlan),
		Assets:     nil,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	artifact, err := proc.Process(ctx, job)
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	if artifact.Kind != "segment" || artifact.ContentType != "video/mp4" {
		t.Fatalf("artifact type: kind=%q content_type=%q", artifact.Kind, artifact.ContentType)
	}
	if artifact.ArtifactHash == "" || artifact.StorageKey != artifact.ArtifactHash {
		t.Fatalf("artifact hash: %+v", artifact)
	}
	if artifact.SizeBytes <= 0 {
		t.Fatalf("artifact size = %d", artifact.SizeBytes)
	}
	if artifact.Backend != "software" {
		t.Fatalf("artifact backend = %q", artifact.Backend)
	}

	// The rendered mp4 was published to the artifact store.
	stored, err := store.Get(ctx, artifact.StorageKey)
	if err != nil {
		t.Fatalf("get published artifact: %v", err)
	}
	if len(stored) == 0 {
		t.Fatalf("published artifact is empty")
	}
	if int64(len(stored)) != artifact.SizeBytes {
		t.Fatalf("published size %d != artifact size %d", len(stored), artifact.SizeBytes)
	}
}
