// Prepare-phase behavior: fail-closed workspace leases, compile-and-store
// without invoking Chronon, warm-up intents and the URL self-heal path with
// its hash-mismatch boundary.
package processor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

// TestPrepareJobFailsClosedWhenLeaseCannotBeEstablished pins the workspace
// liveness invariant: the initial .lease_until marker is what keeps
// CleanupStale from sweeping an active workspace. If WriteLease fails at
// prepare time the job must fail closed (and clean its workspace) instead of
// proceeding without the marker — the previous behavior only logged the
// error and rendered anyway, leaving the live workspace sweepable.
func TestPrepareJobFailsClosedWhenLeaseCannotBeEstablished(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based test; root bypasses file mode checks")
	}
	proc, store, _ := newProcessor(t)
	// Make the whole temp jobs tree readable so the chmod below only needs to
	// tighten the specific job directory.
	if err := os.Chmod(proc.jobsRoot, 0o755); err != nil {
		t.Fatalf("chmod jobs root: %v", err)
	}
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	job := validJob()

	// Make WriteLease fail deterministically: pre-create the job workspace and
	// place a non-empty DIRECTORY at .lease_until, so the marker write hits
	// ENOTDIR/EISDIR no matter the process umask or uid. The workspace tree
	// stays writable, so the fail-closed Cleanup() can still remove it.
	jobDir := filepath.Join(proc.jobsRoot, job.ID)
	if _, err := workspace.New(proc.jobsRoot, job.ID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(jobDir, ".lease_until", "occupied"), 0o755); err != nil {
		t.Fatalf("plant blocking .lease_until dir: %v", err)
	}

	prepared, err := proc.PrepareJob(context.Background(), job)
	if err == nil {
		prepared.Workspace.Cleanup()
		t.Fatal("PrepareJob must fail when the workspace lease cannot be established")
	}
	if !strings.Contains(err.Error(), "establish workspace lease") {
		t.Fatalf("error = %v, want a workspace-lease failure", err)
	}
	// The job failed closed: its workspace must not linger under jobsRoot.
	if _, statErr := os.Stat(jobDir); !os.IsNotExist(statErr) {
		t.Fatalf("workspace not cleaned up after lease failure, stat err = %v", statErr)
	}
}

func TestPrepareCompilesAndStoresPlanWithoutInvokingChronon(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	if err := store.Put(context.Background(), videoHash, []byte("video-bytes")); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	job := validJob()
	job.ID = "overlay-prepare-1"

	artifact, err := proc.Prepare(context.Background(), job)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if renderer.calls != 0 {
		t.Fatalf("prepare invoked Chronon %d times", renderer.calls)
	}
	if artifact.Kind != "overlay_prepare" || artifact.ContentType != "application/json" {
		t.Fatalf("prepare artifact = %+v", artifact)
	}
	if artifact.ArtifactHash == "" || artifact.StorageKey != artifact.ArtifactHash || artifact.SizeBytes <= 0 {
		t.Fatalf("prepare artifact identity incomplete: %+v", artifact)
	}
	data, err := store.Get(context.Background(), artifact.StorageKey)
	if err != nil {
		t.Fatalf("get prepared plan: %v", err)
	}
	if int64(len(data)) != artifact.SizeBytes {
		t.Fatalf("prepared plan size = %d, artifact says %d", len(data), artifact.SizeBytes)
	}
}

func TestPrepareAcceptsPipelineGenOverlayIntentWarmup(t *testing.T) {
	proc, store, renderer := newProcessor(t)
	job := validJob()
	job.ID = "overlay-prepare-intents"
	job.Assets = nil
	job.JobType = queue.JobTypeOverlayPrepare
	job.RenderPlan = json.RawMessage(`{
      "schema_version":"renderinggen.overlay-prepare.v1",
      "plan_id":"run-1","video_id":"run-1",
      "width":1280,"height":720,"fps_num":30,"fps_den":1,
      "intents":[{"template_id":"person_default","timing_state":"PENDING"}]
    }`)

	artifact, err := proc.Prepare(context.Background(), job)
	if err != nil {
		t.Fatalf("prepare intent warmup: %v", err)
	}
	if renderer.calls != 0 {
		t.Fatalf("prepare invoked Chronon %d times", renderer.calls)
	}
	if artifact.Kind != "overlay_prepare" || artifact.SizeBytes <= 0 {
		t.Fatalf("unexpected prepare artifact: %+v", artifact)
	}
	if _, err := store.Get(context.Background(), artifact.StorageKey); err != nil {
		t.Fatalf("prepared intent document was not stored: %v", err)
	}
}

// TestPrepareSelfHealsMissingEntityImageFromURL certifies Gate 4's real gap:
// PipelineGen materializes a catalog entity image to Drive and enqueues
// overlay.prepare BEFORE the bytes are staged in the RenderingGen object
// store. The worker must download the asset from its source URL, verify the
// SHA-256, stage it into L3 and still complete the prepare job — never fail
// with "object not found" on the first attempt.
func TestPrepareSelfHealsMissingEntityImageFromURL(t *testing.T) {
	proc, store, _ := newProcessor(t)

	imageBytes := []byte("michael-jordan-catalog-image-jpeg")
	imageHash := storage.Hash(imageBytes)

	// A real HTTP source for the catalog image; nothing is staged in L3.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(imageBytes)
	}))
	defer srv.Close()

	job := validJob()
	job.ID = "overlay-prepare-self-heal"
	job.JobType = queue.JobTypeOverlayPrepare
	job.Assets = []queue.AssetRef{{Hash: imageHash, LogicalPath: srv.URL + "/michael-jordan.jpg"}}
	job.RenderPlan = json.RawMessage(`{
      "schema_version":"renderinggen.overlay-prepare.v1",
      "plan_id":"run-1","video_id":"run-1",
      "width":1280,"height":720,"fps_num":30,"fps_den":1,
      "intents":[{"template_id":"person_default","timing_state":"PENDING"}]
    }`)

	artifact, err := proc.Prepare(context.Background(), job)
	if err != nil {
		t.Fatalf("prepare must self-heal the missing asset: %v", err)
	}
	if artifact.Kind != "overlay_prepare" {
		t.Fatalf("prepare artifact = %+v", artifact)
	}

	// The downloaded bytes were verified and staged into L3 under their hash.
	staged, err := store.Get(context.Background(), imageHash)
	if err != nil {
		t.Fatalf("self-healed asset was not staged into L3: %v", err)
	}
	if string(staged) != string(imageBytes) {
		t.Fatalf("staged asset = %q, want %q", staged, imageBytes)
	}
}

// TestPrepareSelfHealRejectsHashMismatch pins the fail-closed boundary: a URL
// whose bytes do not hash to the declared asset hash must fail resolution, and
// must never stage the wrong bytes into L3.
func TestPrepareSelfHealRejectsHashMismatch(t *testing.T) {
	proc, store, _ := newProcessor(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wrong-bytes"))
	}))
	defer srv.Close()

	wantHash := storage.Hash([]byte("the-real-bytes"))
	job := validJob()
	job.ID = "overlay-prepare-self-heal-mismatch"
	job.JobType = queue.JobTypeOverlayPrepare
	job.Assets = []queue.AssetRef{{Hash: wantHash, LogicalPath: srv.URL + "/wrong.jpg"}}
	job.RenderPlan = json.RawMessage(`{
      "schema_version":"renderinggen.overlay-prepare.v1",
      "plan_id":"run-1","video_id":"run-1",
      "width":1280,"height":720,"fps_num":30,"fps_den":1,
      "intents":[{"template_id":"person_default","timing_state":"PENDING"}]
    }`)

	if _, err := proc.Prepare(context.Background(), job); err == nil {
		t.Fatal("prepare must fail when the URL bytes do not match the declared hash")
	}
	if _, err := store.Get(context.Background(), wantHash); err == nil {
		t.Fatal("wrong bytes must never be staged into L3")
	}
}
