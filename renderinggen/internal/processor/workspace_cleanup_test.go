package processor

import (
	"os"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workspace"
)

// TestCleanupWorkspaceReportsFailures locks the degradation contract: a
// workspace that cannot be removed is fail-open (the render already succeeded)
// but it must never be invisible. The failure is counted cumulatively and
// surfaced through Degradations(), which /health exposes.
func TestCleanupWorkspaceReportsFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only parent directory does not deny root, so the failure cannot be provoked")
	}
	root := t.TempDir()
	// Restore writability before t.TempDir's own cleanup runs (cleanups are LIFO).
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	proc := &Processor{jobsRoot: root}
	ws, err := workspace.New(root, "job-1")
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}

	// A successful cleanup is not a degradation.
	proc.cleanupWorkspace(ws, "job-1")
	if got := proc.Degradations(); got != nil {
		t.Fatalf("a successful cleanup must not degrade, got %v", got)
	}

	ws, err = workspace.New(root, "job-1")
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	proc.cleanupWorkspace(ws, "job-1")
	got := proc.Degradations()
	if got == nil || got[metricnames.WorkspaceCleanupFailures] != 1 {
		t.Fatalf("want %s=1 after one failure, got %v", metricnames.WorkspaceCleanupFailures, got)
	}
	// The counter is cumulative, not a latch.
	proc.cleanupWorkspace(ws, "job-1")
	if got := proc.Degradations(); got[metricnames.WorkspaceCleanupFailures] != 2 {
		t.Fatalf("want cumulative %s=2, got %v", metricnames.WorkspaceCleanupFailures, got)
	}

	// The reported name must belong to the worker's declared vocabulary: an
	// undeclared name would be persisted as an untyped metric by the queue.
	if !metricnames.Declared(metricnames.WorkspaceCleanupFailures) {
		t.Fatalf("%s is not declared in the metric vocabulary", metricnames.WorkspaceCleanupFailures)
	}
	if unit, ok := metricnames.Unit(metricnames.WorkspaceCleanupFailures); !ok || unit != metricnames.UnitCount {
		t.Fatalf("%s unit = %q/%v, want count", metricnames.WorkspaceCleanupFailures, unit, ok)
	}
}

// TestNoteTimingSidecarMissingRecordsMetric locks the fail-open telemetry
// contract: when the raw timing sidecar is unavailable the artifact still
// publishes, but the absence is recorded as a declared metric so a renderer
// that silently stops emitting the sidecar is visible in the ledger.
func TestNoteTimingSidecarMissingRecordsMetric(t *testing.T) {
	if !metricnames.Declared(metricnames.ChrononTimingSidecarMissing) {
		t.Fatalf("%s is not declared in the metric vocabulary", metricnames.ChrononTimingSidecarMissing)
	}
	if !metricnames.Declared(metricnames.ChrononTelemetryMissing) {
		t.Fatalf("%s is not declared in the metric vocabulary", metricnames.ChrononTelemetryMissing)
	}

	// A nil artifact is a no-op, not a panic (the caller may hold no artifact).
	noteTimingSidecarMissing(nil)

	artifact := &queue.Artifact{}
	noteTimingSidecarMissing(artifact)
	if artifact.Metrics == nil {
		t.Fatal("expected the metric map to be materialized")
	}
	if artifact.Metrics[metricnames.ChrononTimingSidecarMissing] != 1 {
		t.Fatalf("want %s=1, got %v", metricnames.ChrononTimingSidecarMissing, artifact.Metrics)
	}

	// An artifact that already carries metrics keeps them.
	withMetrics := &queue.Artifact{Metrics: map[string]float64{"render_ms": 12}}
	noteTimingSidecarMissing(withMetrics)
	if withMetrics.Metrics["render_ms"] != 12 {
		t.Fatalf("existing metrics were dropped: %v", withMetrics.Metrics)
	}
	if withMetrics.Metrics[metricnames.ChrononTimingSidecarMissing] != 1 {
		t.Fatalf("want %s=1, got %v", metricnames.ChrononTimingSidecarMissing, withMetrics.Metrics)
	}
}
