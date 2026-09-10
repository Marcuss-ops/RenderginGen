// Command batch-submit expands a batch master manifest into N idempotent
// queue jobs and submits them to the central RenderingGen queue.
//
// Two manifest shapes are accepted (auto-detected from schema_version):
//
//   - renderinggen.batch-manifest.v1 (flat): one entry per video;
//   - renderinggen.batch-multilingual.v1 (Strategy A): one base render +
//     one overlay-only render per language. The base is rendered once and
//     every language composites over its artifact — roughly one third of
//     the GPU cost of three full renders.
//
// Idempotency: every derived job's idempotency_key is a SHA-256 over
// (batch_id, logical id, plan bytes, asset refs). Re-running the command with
// the same manifest is safe — the queue resolves each submit to the already
// queued job (HTTP 409 → counted as "existing") instead of rendering twice.
// Editing a plan produces a new key and therefore a new job.
//
// Usage:
//
//	batch-submit -queue http://localhost:8081 -manifest batch.json
//	batch-submit -queue http://localhost:8081 -manifest batch.json -dry-run
//	batch-submit -queue http://localhost:8081 -manifest ml.json -poll 5s
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	queue "github.com/Marcuss-ops/RenderginGen/queue/client"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/batch"
)

// batchJob and manifestRef are aliases so the multilingual runner can be
// tested against the same shapes the expansion returns.
type batchJob = queue.Job

func main() {
	queueURL := flag.String("queue", "http://localhost:8081", "central queue endpoint")
	manifestPath := flag.String("manifest", "", "path to the batch manifest JSON (required)")
	dryRun := flag.Bool("dry-run", false, "expand and validate the manifest without submitting")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall submission budget")
	poll := flag.Duration("poll", 2*time.Second, "poll interval while waiting for the base render (multilingual mode)")
	flag.Parse()

	if *manifestPath == "" {
		fmt.Fprintln(os.Stderr, "batch-submit: -manifest is required")
		flag.Usage()
		os.Exit(2)
	}

	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fatal("read manifest: %v", err)
	}

	jobs, err := batch.Decode(raw)
	if err != nil {
		fatal("expand manifest: %v", err)
	}

	summary := struct {
		Manifest  string   `json:"manifest"`
		BatchJobs int      `json:"jobs"`
		JobIDs    []string `json:"job_ids"`
		DryRun    bool     `json:"dry_run"`
	}{Manifest: *manifestPath, BatchJobs: len(jobs), DryRun: *dryRun}
	for _, j := range jobs {
		summary.JobIDs = append(summary.JobIDs, j.ID)
	}

	if *dryRun {
		out, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(out))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	submitter := batch.ClientSubmitter{Client: queue.New(*queueURL)}

	if isMultilingual(raw) {
		ids := manifestIdentifiersOf(raw)
		res, err := runMultilingual(ctx, submitter, jobs, ids, *poll)
		if err != nil {
			fatal("multilingual submit: %v", err)
		}
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return
	}

	res, err := batch.SubmitAll(ctx, submitter, jobs)
	if err != nil {
		fatal("submit: %v (submitted=%v existing=%v)", err, res.Submitted, res.Existing)
	}

	out, _ := json.MarshalIndent(struct {
		Manifest  string   `json:"manifest"`
		Jobs      int      `json:"jobs"`
		Submitted []string `json:"submitted"`
		Existing  []string `json:"existing"`
	}{Manifest: *manifestPath, Jobs: len(jobs), Submitted: res.Submitted, Existing: res.Existing}, "", "  ")
	fmt.Println(string(out))
}

// manifestIdentifiers extracts the batch id and base logical id from a
// multilingual manifest (already schema-validated by isMultilingual).
type manifestIdentifiers struct {
	batchID       batch.BatchID
	baseLogicalID string
}

func manifestIdentifiersOf(raw []byte) manifestIdentifiers {
	var probe struct {
		BatchID string `json:"batch_id"`
		Base    struct {
			ID string `json:"id"`
		} `json:"base"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		fatal("decode multilingual manifest identifiers: %v", err)
	}
	return manifestIdentifiers{batchID: batch.BatchID(probe.BatchID), baseLogicalID: probe.Base.ID}
}

func isMultilingual(raw []byte) bool {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.SchemaVersion == batch.SchemaMultilingualBatchV1
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "batch-submit: "+format+"\n", args...)
	os.Exit(1)
}
