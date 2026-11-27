package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// TestStoreArtifactDoesNotPublishBytesRejectedBeforeCertification pins the
// publish ordering: the object store must not receive bytes for a render that
// is rejected by a later certification step (here, the composition
// cross-check under the strict policy). Before the reorder the artifact was
// PUT first and then rejected, leaving an object for a job that never got a
// ledger row.
func TestStoreArtifactDoesNotPublishBytesRejectedBeforeCertification(t *testing.T) {
	backend := storage.NewMemory()
	proc := &Processor{
		store:          storage.New(backend, storage.Options{}),
		receiptVerify:  renderVerifyStrict,
		chrononVersion: "test",
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(output, []byte("rendered bytes that must not be published"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A plan with an authored overlay, plus a sidecar that says the engine
	// took the single-source path: the composition check rejects it.
	sidecar, err := json.Marshal(map[string]any{
		"schema":  chronon.TimingSidecarSchemaV2,
		"version": 2,
		"job": map[string]any{
			"execution_path": chronon.ExecutionPathDirectYUV,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output+chronon.TimingSidecarSuffix, sidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := &overlay.Plan{
		Canvas: overlay.Canvas{Width: 64, Height: 64, FPSNum: 25, FPSDen: 1, DurationFrames: 25},
		Layers: []overlay.Layer{
			{ID: "source", Type: "video"},
			{ID: "title", Type: "text", Text: "authored overlay"},
		},
	}

	_, err = proc.storeArtifact(context.Background(), storeArtifactInput{
		jobID:        "rejected-before-publish",
		outputPath:   output,
		plan:         plan,
		phaseMetrics: map[string]float64{},
		totalStart:   time.Now(),
	})
	if err == nil {
		t.Fatal("strict policy accepted a composition divergence")
	}

	hash, _, hashErr := hashio.File(output)
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	if _, fetchErr := backend.Fetch(context.Background(), hash); fetchErr == nil {
		t.Fatalf("artifact %s was published before certification rejected it", hash)
	}
}
