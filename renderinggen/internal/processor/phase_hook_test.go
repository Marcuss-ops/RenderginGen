package processor

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// TestPhaseHookObservesOverlayCompileAndSHA256 pins the worker-metrics wiring:
// overlay_compile and sha256 must reach the processor's PhaseHook (which is
// what feeds `renderinggen_worker_phase_duration_seconds` on /metrics), not
// only the artifact-ledger metrics map. Before this test the two phases were
// written straight into the metrics map, so a Prometheus scrape could show a
// render/publish histogram while the CPU-bound compile and hash halves of the
// job were invisible — exactly the phases needed to decide whether the Go
// worker is on the render's critical path.
func TestPhaseHookObservesOverlayCompileAndSHA256(t *testing.T) {
	phases := map[string]time.Duration{}
	proc, _, renderer := newProcessorWith(t, Options{
		PhaseHook: func(phase string, d time.Duration) {
			phases[phase] = d
		},
	})

	job := &queue.Job{
		ID:      "phase-hook-job",
		Schema:  queue.JobSchemaV1,
		Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"phase-hook-job","video_id":"video-1",
          "width":1280,"height":720,"fps_num":30,"fps_den":1,
          "items":[
            {"id":"phrase-1","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"Hello world","start_ms":0,"end_ms":1000}
          ]
        }`),
	}
	renderer.write = func(path string) error {
		return os.WriteFile(path, []byte("output-bytes"), 0o644)
	}

	if _, err := proc.Process(context.Background(), job); err != nil {
		t.Fatalf("process: %v", err)
	}

	// The two phases this change routes through recordPhase: the CPU-bound
	// compile half and the output hashing pass. Both must be observed with a
	// non-negative duration (a zero duration is legal for the receipt-proven
	// identity path, where no re-read happens; an ABSENT series is not).
	for _, phase := range []string{"overlay_compile", "sha256"} {
		if _, ok := phases[phase]; !ok {
			t.Errorf("phase %q was not observed by the PhaseHook (observed %v); it would be missing from the worker's Prometheus phase histogram", phase, keysOf(phases))
		}
	}
	// Sanity: the phases that already fed the hook keep feeding it, so this
	// test cannot pass while the whole hook is dead.
	for _, phase := range []string{"render", "publish"} {
		if _, ok := phases[phase]; !ok {
			t.Errorf("phase %q was not observed by the PhaseHook (observed %v)", phase, keysOf(phases))
		}
	}
}

func keysOf(m map[string]time.Duration) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
