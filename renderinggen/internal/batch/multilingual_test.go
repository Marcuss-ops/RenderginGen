// multilingual_test.go locks the Strategy-A binding contract: the overlay
// plans are patched with the base artifact hash, the base artifact rides as
// an ordinary content-addressed asset, keys are recomputed over patched
// content, and a terminal-failed base aborts the wave.
package batch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
)

func TestBindOverlayJobsPatchesPlanAndAssets(t *testing.T) {
	jobs, err := ExpandMultilingual(multilingualManifest())
	if err != nil {
		t.Fatal(err)
	}
	base := BaseOutcome{JobID: jobs[0].ID, ArtifactHash: "basehash123"}

	bound, err := BindOverlayJobs(BatchID("ml-batch-1"), jobs, base)
	if err != nil {
		t.Fatalf("BindOverlayJobs: %v", err)
	}
	if len(bound) != 2 {
		t.Fatalf("bound jobs = %d, want 2 (base dropped)", len(bound))
	}
	for _, j := range bound {
		// The plan's source block must now carry the base hash.
		var plan struct {
			Source struct {
				AssetID string `json:"asset_id"`
				SHA256  string `json:"sha256"`
			} `json:"source"`
		}
		if err := json.Unmarshal(j.RenderPlan, &plan); err != nil {
			t.Fatalf("decode patched plan: %v", err)
		}
		if plan.Source.AssetID != "bound_base" || plan.Source.SHA256 != "basehash123" {
			t.Fatalf("job %s source = %+v", j.ID, plan.Source)
		}
		// The base artifact must ride as an ordinary asset at the bind path.
		found := false
		for _, a := range j.Assets {
			if a.Hash == "basehash123" && a.LogicalPath == SourceBindLogicalPath {
				found = true
			}
		}
		if !found {
			t.Fatalf("job %s carries no bound base asset: %+v", j.ID, j.Assets)
		}
	}
}

func TestBindOverlayJobsRekeysDeterministically(t *testing.T) {
	jobs, _ := ExpandMultilingual(multilingualManifest())
	base := BaseOutcome{JobID: jobs[0].ID, ArtifactHash: "basehash123"}

	first, err := BindOverlayJobs(BatchID("ml-batch-1"), jobs, base)
	if err != nil {
		t.Fatal(err)
	}
	// Re-expand (fresh copy) and bind again: identical patched content must
	// produce identical keys — a replay resolves to the same jobs.
	jobs2, _ := ExpandMultilingual(multilingualManifest())
	second, err := BindOverlayJobs(BatchID("ml-batch-1"), jobs2, base)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i].IdempotencyKey == "" {
			t.Fatalf("job %d lost its key", i)
		}
		if first[i].IdempotencyKey != second[i].IdempotencyKey {
			t.Fatalf("bound key not deterministic for job %d", i)
		}
	}
	// A different base hash (re-rendered base) must yield different keys.
	base2 := BaseOutcome{JobID: jobs[0].ID, ArtifactHash: "otherhash"}
	jobs3, _ := ExpandMultilingual(multilingualManifest())
	third, _ := BindOverlayJobs(BatchID("ml-batch-1"), jobs3, base2)
	if third[0].IdempotencyKey == first[0].IdempotencyKey {
		t.Fatal("new base hash must produce new overlay keys")
	}
}

func TestAwaitBaseCompleted(t *testing.T) {
	s := stubSubmitter{job: queue.Job{
		ID:       "ml-batch-1:base-001",
		State:    queue.StateCompleted,
		Artifact: &queue.Artifact{ArtifactHash: "abc"},
	}}
	out, err := AwaitBase(context.Background(), s, "ml-batch-1:base-001", time.Millisecond)
	if err != nil {
		t.Fatalf("AwaitBase: %v", err)
	}
	if out.ArtifactHash != "abc" {
		t.Fatalf("hash = %q", out.ArtifactHash)
	}
}

func TestAwaitBaseAbortsOnFailedBase(t *testing.T) {
	s := stubSubmitter{job: queue.Job{ID: "b", State: queue.StateFailed}}
	if _, err := AwaitBase(context.Background(), s, "b", time.Millisecond); err == nil {
		t.Fatal("failed base must abort the wave")
	}
}

func TestAwaitBaseAbortsOnContextCancel(t *testing.T) {
	s := stubSubmitter{job: queue.Job{ID: "b", State: queue.StatePending}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := AwaitBase(ctx, s, "b", time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestOverlayPlanSourcePatchRejectsGarbage(t *testing.T) {
	if _, err := overlayPlanSourcePatch([]byte(`not-json`), "h"); err == nil {
		t.Fatal("non-JSON plan must be rejected")
	}
	if _, err := overlayPlanSourcePatch([]byte(`[]`), "h"); err == nil {
		t.Fatal("non-object plan must be rejected")
	}
	// A plan without a source block gains one (first overlay pass over a
	// synthetic background still needs the bound base video).
	patched, err := overlayPlanSourcePatch([]byte(`{"schema_version":"renderinggen.overlay-plan.v1"}`), "h")
	if err != nil {
		t.Fatalf("plan without source: %v", err)
	}
	if !strings.Contains(string(patched), `"sha256":"h"`) {
		t.Fatalf("patched plan missing base hash: %s", patched)
	}
}
