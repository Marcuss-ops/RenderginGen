package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// words is the 10-text corpus for the cache benchmark: every job renders the
// same GoldenOverlayJobV1 workload (background.jpg + apple.png + phrase) with
// a different kinetic word, so only the text layer differs between jobs.
var words = []string{"APPLE", "TESLA", "NVIDIA", "AMD", "INTEL", "SAMSUNG", "GOOGLE", "META", "AMAZON", "MICROSOFT"}

// jobResult holds the recorded metrics for one benchmark job.
type jobResult struct {
	Word            string
	TotalMS         float64
	MaterializeMS   float64 // asset fetch
	PlanMS          float64 // plan.json write
	RenderMS        float64 // chronon3d_cli subprocess
	PublishMS       float64
	RenderEngineMS  float64 // from telemetry JSONL (--report)
	EncodeMS        float64 // from telemetry JSONL (--report)
	EngineCacheHits int64
	EngineCacheMiss int64
	L1Hits          int64 // worker asset cache
	L2Hits          int64
	L3Fetches       int64
}

// telemetryLine is the subset of the render_history.jsonl run record the
// benchmark needs (fields written by write_run_to_jsonl in Chronon3d).
type telemetryLine struct {
	RenderMS    float64 `json:"render_ms"`
	EncodeMS    float64 `json:"encode_ms"`
	CacheHits   int64   `json:"cache_hits"`
	CacheMisses int64   `json:"cache_misses"`
}

// goldenJobWithWord decodes the canonical GoldenOverlayJobV1 and rewrites the
// important_word text layer, keeping everything else identical.
func goldenJobWithWord(t *testing.T, word string) *queue.Job {
	t.Helper()
	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenOverlayJobV1), &job); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	var plan map[string]any
	if err := json.Unmarshal(job.RenderPlan, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	layers, ok := plan["layers"].([]any)
	if !ok {
		t.Fatal("plan.layers is not an array")
	}
	found := false
	for _, l := range layers {
		layer, ok := l.(map[string]any)
		if !ok {
			continue
		}
		if layer["id"] == "important_word" {
			layer["text"] = word
			found = true
		}
	}
	if !found {
		t.Fatal("important_word layer not found in golden plan")
	}
	rewritten, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	job.RenderPlan = rewritten
	return &job
}

// readLastTelemetry reads the last run record appended to the telemetry JSONL
// written by chronon3d_cli --report under CHRONON3D_TELEMETRY_PATH.
func readLastTelemetry(t *testing.T, dir string) telemetryLine {
	t.Helper()
	path := filepath.Join(dir, "render_history.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry %s: %v (is CHRONON3D_TELEMETRY_PATH set for the CLI?)", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var last telemetryLine
	line := lines[len(lines)-1]
	if err := json.Unmarshal([]byte(line), &last); err != nil {
		t.Fatalf("decode last telemetry line: %v\n%s", err, line)
	}
	return last
}

// TestBenchmarkCachePromotion10Jobs runs the real chain 10 times over the
// same assets with different texts, sharing one worker asset cache, and
// verifies:
//
//	job 1:  L3 -> L2 -> L1 (cold: every asset fetched from L3 once)
//	jobs 2-10: L1 cache hit (no L3 fetch, no L2 read)
//
// It also records asset fetch / plan / render / publish / total ms per job and
// the engine-side render_ms / encode_ms / cache hits-misses from the telemetry
// JSONL, then prints a table. Skips when chronon3d_cli is not installed.
func TestBenchmarkCachePromotion10Jobs(t *testing.T) {
	home := os.Getenv("CHRONON_HOME")
	if home == "" {
		home = "/opt/chronon3d"
	}
	cli := &chronon.Client{Home: home}
	if err := cli.Verify(); err != nil {
		t.Skipf("chronon3d_cli not available: %v", err)
	}

	// Point the CLI's telemetry JSONL at a scratch dir so --report lands there.
	telemetryDir := t.TempDir()
	t.Setenv("CHRONON3D_TELEMETRY_PATH", telemetryDir)

	// One shared cache: L1 in memory + L2 on disk + L3 memory backend. The
	// golden assets are seeded into L3 once; promotion happens across jobs.
	store := storage.New(storage.NewMemory(), storage.Options{
		L1MaxBytes: 1 << 20,
		L2Dir:      filepath.Join(t.TempDir(), "l2"),
		L2MaxBytes: 1 << 20,
	})
	proc := New(t.TempDir(), "software", cli.Version(), "http://store:9000", store, cli)
	proc.SetReport(true)

	// Seed the two deterministic fixtures into L3 under their content hashes.
	seedGoldenAssets(t, store, mustGoldenAssets(t))

	var results []jobResult
	for i, word := range words {
		job := goldenJobWithWord(t, word)
		job.ID = fmt.Sprintf("bench-%02d-%s", i+1, word)
		job.RenderPlan = rewriteJobID(t, job.RenderPlan, job.ID)

		var phases = map[string]float64{}
		proc.SetPhaseHook(func(phase string, d time.Duration) {
			phases[phase] = float64(d.Microseconds()) / 1000.0
		})

		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		artifact, err := proc.Process(ctx, job)
		cancel()
		if err != nil {
			t.Fatalf("job %d (%s): %v", i+1, word, err)
		}
		total := float64(time.Since(start).Microseconds()) / 1000.0

		stats := store.Stats()
		tel := readLastTelemetry(t, telemetryDir)
		results = append(results, jobResult{
			Word:            word,
			TotalMS:         total,
			MaterializeMS:   phases["materialize"],
			PlanMS:          phases["plan"],
			RenderMS:        phases["render"],
			PublishMS:       phases["publish"],
			RenderEngineMS:  tel.RenderMS,
			EncodeMS:        tel.EncodeMS,
			EngineCacheHits: tel.CacheHits,
			EngineCacheMiss: tel.CacheMisses,
			L1Hits:          stats.L1Hits,
			L2Hits:          stats.L2Hits,
			L3Fetches:       stats.L3Fetches,
		})
		_ = artifact
	}

	// ── Verifications ─────────────────────────────────────────────────────
	r0 := results[0]
	// Job 1 (cold): both assets fetched exactly once from L3.
	if r0.L3Fetches != 2 {
		t.Fatalf("job 1: want 2 L3 fetches (background+apple), got %d (L1=%d L2=%d)", r0.L3Fetches, r0.L1Hits, r0.L2Hits)
	}
	if r0.L1Hits != 0 || r0.L2Hits != 0 {
		t.Fatalf("job 1 should be cold (0 L1/L2 hits), got L1=%d L2=%d", r0.L1Hits, r0.L2Hits)
	}
	// Jobs 2-10: cache hits, no additional L3 fetch or L2 read.
	for i, r := range results[1:] {
		if r.L3Fetches != 2 {
			t.Fatalf("job %d (%s): L3 fetches grew to %d (want 2 = job-1 cold only)", i+2, r.Word, r.L3Fetches)
		}
		if r.L1Hits < int64(i+1)*2 {
			t.Fatalf("job %d (%s): L1 hits = %d, want >= %d (2 assets x prior jobs)", i+2, r.Word, r.L1Hits, (i+1)*2)
		}
	}

	// ── Report ────────────────────────────────────────────────────────────
	fmt.Printf("\n=== RenderingGen cache benchmark: 10 jobs, same assets, different texts ===\n")
	fmt.Printf("%-10s %9s %10s %8s %10s %9s %9s %9s %6s %7s %8s %7s %7s\n",
		"word", "total_ms", "mat_ms", "plan_ms", "render_ms", "pub_ms", "eng_r_ms", "enc_ms", "eng_h", "eng_m", "L1_hit", "L2_hit", "L3_fetch")
	for i, r := range results {
		label := fmt.Sprintf("%02d:%s", i+1, r.Word)
		fmt.Printf("%-10s %9.1f %10.2f %8.2f %10.1f %9.2f %9.1f %9.1f %6d %7d %8d %7d %7d\n",
			label, r.TotalMS, r.MaterializeMS, r.PlanMS, r.RenderMS, r.PublishMS,
			r.RenderEngineMS, r.EncodeMS, r.EngineCacheHits, r.EngineCacheMiss,
			r.L1Hits, r.L2Hits, r.L3Fetches)
	}
	fmt.Println("(eng_r_ms/enc_ms/eng_h/eng_m come dalla telemetria del motore; L1/L2/L3 sono la cache asset del worker)")
}

// mustGoldenAssets decodes the canonical payload to extract the asset refs.
func mustGoldenAssets(t *testing.T) []queue.AssetRef {
	t.Helper()
	var job queue.Job
	if err := json.Unmarshal([]byte(chronon.GoldenOverlayJobV1), &job); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	return job.Assets
}

// rewriteJobID updates the job_id inside the render plan so each benchmark
// job has a distinct composition identity.
func rewriteJobID(t *testing.T, plan []byte, id string) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(plan, &m); err != nil {
		t.Fatalf("decode plan for job id rewrite: %v", err)
	}
	m["job_id"] = id
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal plan with job id: %v", err)
	}
	return out
}
