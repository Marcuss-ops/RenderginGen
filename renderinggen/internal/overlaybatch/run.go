package overlaybatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/batch"
)

// ReportSchema identifies the run report this package writes.
const ReportSchema = "renderinggen.batch-run-report.v1"

// Options configures one batch run.
type Options struct {
	// ManifestPath is the renderinggen.batch-manifest.v1 document of record.
	ManifestPath string
	// QueueURL is the central queue endpoint.
	QueueURL string
	// ReportPath is where the run report is written (required).
	ReportPath string
	// SnapshotPath is where the final queue bodies are preserved. Empty skips it.
	SnapshotPath string
	// DownloadDir collects the certified MP4s, one subdirectory per family.
	DownloadDir string
	// TimingDir collects the raw per-frame timing sidecars. Empty skips them.
	TimingDir string
	// Timeout bounds the whole run (submit + render + download).
	Timeout time.Duration
	// Concurrency is how many jobs are waited on and downloaded at once.
	Concurrency int
	// ServeDir, when set, is served over plain HTTP for the duration of the run.
	// The worker self-heals a missing asset from the manifest's source_url, and
	// a local corpus needs a URL that answers: serving it here keeps the whole
	// run in this binary instead of shelling out to a separate file server.
	ServeDir string
	// ServeAddr is the listen address for ServeDir (e.g. 127.0.0.1:8099).
	ServeAddr string
	// Logger receives progress lines. Nil logs to stderr.
	Logger *log.Logger
}

// Stats summarizes a millisecond series.
type Stats struct {
	Count  int     `json:"count"`
	MinMS  float64 `json:"min_ms"`
	P50MS  float64 `json:"p50_ms"`
	MaxMS  float64 `json:"max_ms"`
	MeanMS float64 `json:"mean_ms"`
}

// JobReport is one job's outcome, timings and artifact reference.
type JobReport struct {
	JobID string `json:"job_id"`
	// LogicalID is the manifest-local id (the queue id is batch:logical).
	LogicalID string `json:"logical_id"`
	Family    string `json:"family,omitempty"`
	Template  string `json:"template_id,omitempty"`
	PresetID  string `json:"preset_id,omitempty"`
	MotionID  string `json:"motion_id,omitempty"`
	Language  string `json:"language,omitempty"`
	Text      string `json:"text,omitempty"`
	EntityID  string `json:"entity_id,omitempty"`
	// EntityName, Asset, AssetSource and AssetAttribution record the image
	// corpus provenance (which portrait bytes were rendered, and where from).
	EntityName       string `json:"entity_name,omitempty"`
	Asset            string `json:"asset,omitempty"`
	AssetSource      string `json:"asset_source,omitempty"`
	AssetAttribution string `json:"asset_attribution,omitempty"`
	// EntranceDuration* state the preset's reveal window when the corpus
	// declares one.
	EntranceDurationFrames  *int     `json:"entrance_duration_frames,omitempty"`
	EntranceDurationSeconds *float64 `json:"entrance_duration_seconds,omitempty"`

	State        string     `json:"state"`
	Attempts     int        `json:"attempts,omitempty"`
	QueuedAt     *time.Time `json:"queued_at,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	QueueWaitMS  *int64     `json:"queue_wait_ms,omitempty"`
	RenderWallMS *int64     `json:"render_wall_ms,omitempty"`
	FailReason   string     `json:"fail_reason,omitempty"`

	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
	ArtifactURL    string `json:"artifact_url,omitempty"`
	ArtifactBytes  int64  `json:"artifact_bytes,omitempty"`
	Backend        string `json:"backend,omitempty"`

	// LocalVideo/LocalTiming are the downloaded copies, relative to the working
	// directory the run was started from.
	LocalVideo  string `json:"local_video,omitempty"`
	LocalTiming string `json:"local_timing,omitempty"`
	// Chronon* are the raw renderer timings read back from the sidecar.
	ChrononWallTimeMS  *float64 `json:"chronon_wall_time_ms,omitempty"`
	ChrononRenderMS    *float64 `json:"chronon_render_ms,omitempty"`
	ChrononFramesTotal *int     `json:"chronon_frames_total,omitempty"`
}

// Summary is the aggregate of one run. Every number is derived from a queue
// timestamp or a certified artifact fact; nothing is estimated.
type Summary struct {
	JobsTotal      int      `json:"jobs_total"`
	Completed      int      `json:"completed"`
	Failed         int      `json:"failed"`
	Cancelled      int      `json:"cancelled"`
	NotTerminal    int      `json:"not_terminal"`
	PhraseOverlays int      `json:"phrase_overlays"`
	ImageOverlays  int      `json:"image_overlays"`
	TimingSidecars int      `json:"timing_sidecars"`
	QueueWallMS    *int64   `json:"queue_wall_ms,omitempty"`
	RenderWall     *Stats   `json:"render_wall,omitempty"`
	QueueWait      *Stats   `json:"queue_wait,omitempty"`
	ChrononWall    *Stats   `json:"chronon_wall_time,omitempty"`
	DistinctHashes int      `json:"distinct_artifact_hashes"`
	Failures       []string `json:"failures,omitempty"`
}

// Report is the run report document.
type Report struct {
	Schema      string      `json:"schema"`
	BatchID     string      `json:"batch_id"`
	GeneratedAt time.Time   `json:"generated_at"`
	Manifest    string      `json:"manifest"`
	Queue       string      `json:"queue"`
	Jobs        []JobReport `json:"jobs"`
	Summary     Summary     `json:"summary"`
}

// Run executes one batch: submit, wait, collect, report.
//
// A job that never reaches a terminal state is reported as NOT TERMINAL and the
// run fails: a missing render must never read as a success.
func Run(ctx context.Context, opts Options) (*Report, error) {
	if strings.TrimSpace(opts.ManifestPath) == "" {
		return nil, errors.New("overlaybatch: -manifest is required")
	}
	if strings.TrimSpace(opts.ReportPath) == "" {
		return nil, errors.New("overlaybatch: -report is required")
	}
	if opts.QueueURL == "" {
		opts.QueueURL = "http://localhost:8081"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Minute
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "batch-run: ", log.LstdFlags)
	}

	raw, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("overlaybatch: read manifest: %w", err)
	}
	probe, err := decodeProbe(raw)
	if err != nil {
		return nil, err
	}
	// The queue jobs come from the shared expansion, so this run has the same
	// ids and idempotency keys cmd/batch-submit would derive from the same file.
	jobs, err := batch.Decode(raw)
	if err != nil {
		return nil, err
	}

	client := queue.New(opts.QueueURL)
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		return nil, fmt.Errorf("overlaybatch: queue %s is not healthy: %w", opts.QueueURL, err)
	}

	stopServe, err := serveAssets(opts, logger)
	if err != nil {
		return nil, err
	}
	defer stopServe()

	res, err := submitAll(ctx, client, jobs, logger)
	if err != nil {
		return nil, fmt.Errorf("overlaybatch: submit (submitted=%v existing=%v): %w", res.Submitted, res.Existing, err)
	}
	logger.Printf("submitted %d job(s), %d already queued, %d confirmed by read-back (batch %s)",
		len(res.Submitted), len(res.Existing), len(res.Confirmed), probe.BatchID)

	final, err := waitAll(ctx, client, jobs, opts.Concurrency, logger)
	if err != nil {
		return nil, err
	}

	meta := make(map[string]reportProbe, len(probe.Jobs))
	for _, job := range probe.Jobs {
		meta[job.ID] = job
	}

	report := &Report{
		Schema:      ReportSchema,
		BatchID:     probe.BatchID,
		GeneratedAt: time.Now().UTC(),
		Manifest:    opts.ManifestPath,
		Queue:       opts.QueueURL,
		Jobs:        make([]JobReport, 0, len(jobs)),
	}
	httpClient := &http.Client{Timeout: 10 * time.Minute}
	for _, job := range jobs {
		logical := strings.TrimPrefix(job.ID, probe.BatchID+":")
		row, err := buildRow(opts, job, logical, meta[logical], final[job.ID], httpClient)
		if err != nil {
			return nil, err
		}
		report.Jobs = append(report.Jobs, row)
	}
	report.Summary = summarize(report.Jobs)

	if opts.SnapshotPath != "" {
		if err := writeJSON(opts.SnapshotPath, final); err != nil {
			return nil, err
		}
	}
	if err := writeJSON(opts.ReportPath, report); err != nil {
		return nil, err
	}

	printSummary(logger, report)
	if len(report.Summary.Failures) > 0 {
		return report, fmt.Errorf("overlaybatch: %d job(s) did not complete: %s",
			len(report.Summary.Failures), strings.Join(report.Summary.Failures, ", "))
	}
	return report, nil
}

// submitAll submits every job, keeping the shared expansion's semantics (a 409 is
// an idempotent replay) and adding one confirmation: a submit error is resolved
// against the queue by reading the job back, because the queue's idempotent
// submit writes the row and then reads it — a read glitch there returns 500
// while the job is durably queued. Reporting that as a failed submission would
// abandon a batch half-submitted, which is exactly the failure mode this loop
// exists to avoid.
func submitAll(ctx context.Context, client *queue.Client, jobs []queue.Job, logger *log.Logger) (submitResult, error) {
	res := submitResult{}
	for _, job := range jobs {
		err := client.Submit(ctx, job)
		switch {
		case err == nil:
			res.Submitted = append(res.Submitted, job.ID)
		case errors.Is(err, queue.ErrJobExists):
			if _, getErr := client.Get(ctx, job.ID); getErr != nil {
				return res, fmt.Errorf("job %s reported existing but cannot be read back: %w", job.ID, getErr)
			}
			res.Existing = append(res.Existing, job.ID)
		default:
			body, getErr := client.Get(ctx, job.ID)
			if getErr != nil || body.ID == "" {
				return res, fmt.Errorf("submit job %s: %w", job.ID, err)
			}
			logger.Printf("job %s: submit reported %v but the job is queued (state=%s); continuing", job.ID, err, body.State)
			res.Confirmed = append(res.Confirmed, job.ID)
		}
	}
	return res, nil
}

// submitResult distinguishes a job this call enqueued, one that was already
// queued, and one whose submit response failed while the job itself is queued.
type submitResult struct {
	Submitted []string
	Existing  []string
	Confirmed []string
}

// waitAll blocks until every job is terminal, bounded by opts.Concurrency.
// It returns the final queue body of every job it observed.
func waitAll(ctx context.Context, client *queue.Client, jobs []queue.Job, concurrency int, logger *log.Logger) (map[string]queue.Job, error) {
	final := make(map[string]queue.Job, len(jobs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	var firstErr error
	var errOnce sync.Once
	var done int

	for _, job := range jobs {
		wg.Add(1)
		go func(job queue.Job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			body, err := client.WaitTerminal(ctx, job.ID)
			mu.Lock()
			if err != nil {
				errOnce.Do(func() { firstErr = fmt.Errorf("overlaybatch: wait %s: %w", job.ID, err) })
			} else {
				final[job.ID] = body
			}
			done++
			progress := done
			mu.Unlock()
			if progress%5 == 0 || progress == len(jobs) {
				logger.Printf("terminal %d/%d", progress, len(jobs))
			}
		}(job)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return final, nil
}

// buildRow turns one job's final queue body into a report row, downloading the
// certified artifact (and its timing sidecar) when the job completed.
func buildRow(opts Options, job queue.Job, logicalID string, meta reportProbe, body queue.Job, httpClient *http.Client) (JobReport, error) {
	facts := meta.facts()
	row := JobReport{
		JobID:                   job.ID,
		LogicalID:               logicalID,
		Family:                  facts.family,
		Template:                facts.template,
		PresetID:                facts.preset,
		MotionID:                facts.motion,
		Language:                facts.language,
		Text:                    facts.text,
		EntityID:                facts.entityID,
		EntityName:              facts.entityName,
		Asset:                   facts.asset,
		AssetSource:             facts.assetSource,
		AssetAttribution:        facts.attribution,
		EntranceDurationFrames:  facts.entranceF,
		EntranceDurationSeconds: facts.entranceS,
		State:                   string(body.State),
		Attempts:                body.Attempts,
		FailReason:              body.FailReason,
	}
	if row.State == "" {
		row.State = "not_found"
	}
	row.QueuedAt = timePtr(body.QueuedAt)
	row.StartedAt = timePtr(body.StartedAt)
	row.CompletedAt = timePtr(body.CompletedAt)
	row.QueueWaitMS = deltaMS(body.QueuedAt, body.StartedAt)
	row.RenderWallMS = deltaMS(body.StartedAt, body.CompletedAt)

	artifact := body.Artifact
	if artifact != nil {
		row.ArtifactSHA256 = artifact.ArtifactHash
		row.ArtifactURL = artifact.ArtifactURL
		row.ArtifactBytes = artifact.SizeBytes
		row.Backend = artifact.Backend
	}
	if body.State != queue.StateCompleted || artifact == nil || artifact.ArtifactURL == "" {
		return row, nil
	}

	videoPath := ""
	if opts.DownloadDir != "" {
		videoPath = filepath.Join(opts.DownloadDir, downloadFolder(facts.family), artifactFileName(job.ID)+".mp4")
	}
	if videoPath != "" {
		if err := download(httpClient, artifact.ArtifactURL, videoPath, artifact.ArtifactHash); err != nil {
			return row, fmt.Errorf("overlaybatch: download %s: %w", job.ID, err)
		}
		row.LocalVideo = videoPath
	}
	// The raw per-frame timing sidecar is diagnostics, not a deliverable: the
	// repository's artifact policy keeps it on disk beside the render (where the
	// *.mp4.timing.json ignore rule covers it) instead of committing hundreds of
	// megabytes of frame times. -timing-dir overrides the location.
	timingDir := opts.TimingDir
	if timingDir == "" {
		timingDir = filepath.Dir(videoPath)
	}
	if timingDir == "." && videoPath == "" {
		return row, nil
	}
	if artifact.ChrononTimingURL == "" {
		return row, nil
	}
	timingPath := filepath.Join(timingDir, artifactFileName(job.ID)+timingSuffix)
	if err := download(httpClient, artifact.ChrononTimingURL, timingPath, artifact.ChrononTimingSHA256); err != nil {
		return row, fmt.Errorf("overlaybatch: download timing for %s: %w", job.ID, err)
	}
	row.LocalTiming = timingPath
	readChrononTiming(timingPath, &row)
	return row, nil
}

// artifactFileName turns a queue job id into the artifact's file name, using the
// corpus convention <batch>-<logical> so a downloaded render names the batch it
// came from (several runs of one corpus can sit side by side).
func artifactFileName(queueID string) string {
	return strings.ReplaceAll(queueID, ":", "-")
}

// timingSuffix is the raw sidecar's name next to its render.
const timingSuffix = ".mp4.timing.json"

// readChrononTiming lifts the renderer's own wall/render timings out of the raw
// sidecar. A sidecar that cannot be read leaves the fields unset rather than
// reporting a fabricated zero.
func readChrononTiming(path string, row *JobReport) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var timing struct {
		WallTimeMS  *float64 `json:"wall_time_ms"`
		RenderMS    *float64 `json:"render_ms"`
		FramesTotal *int     `json:"frames_total"`
	}
	if json.Unmarshal(raw, &timing) != nil {
		return
	}
	row.ChrononWallTimeMS = timing.WallTimeMS
	row.ChrononRenderMS = timing.RenderMS
	row.ChrononFramesTotal = timing.FramesTotal
}

// download fetches url to path and certifies the bytes against expectedSHA256.
// A mismatch deletes the file and fails: an artifact whose bytes do not hash to
// its content address is not the render the queue certified.
func download(client *http.Client, url, path, expectedSHA256 string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	tmp := path + ".part"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(file, digest), resp.Body); err != nil {
		file.Close()
		os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	got := hex.EncodeToString(digest.Sum(nil))
	if expectedSHA256 != "" && !strings.EqualFold(got, expectedSHA256) {
		os.Remove(tmp)
		return fmt.Errorf("content address mismatch: got %s want %s", got, expectedSHA256)
	}
	return os.Rename(tmp, path)
}

// serveAssets starts the optional local asset server. The returned stop function
// is always safe to call.
func serveAssets(opts Options, logger *log.Logger) (func(), error) {
	if strings.TrimSpace(opts.ServeDir) == "" {
		return func() {}, nil
	}
	addr := opts.ServeAddr
	if addr == "" {
		addr = "127.0.0.1:8099"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("overlaybatch: serve assets on %s: %w", addr, err)
	}
	server := &http.Server{Handler: http.FileServer(http.Dir(opts.ServeDir)), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("asset server stopped: %v", err)
		}
	}()
	logger.Printf("serving assets from %s at http://%s/", opts.ServeDir, listener.Addr())
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}, nil
}

// summarize derives the aggregate. Counts come from the rows; the wall window
// comes from the queue timestamps, never from a stopwatch.
func summarize(rows []JobReport) Summary {
	summary := Summary{JobsTotal: len(rows)}
	var queued, completed []time.Time
	var renderWall, queueWait, chrononWall []float64
	hashes := map[string]bool{}
	for _, row := range rows {
		switch row.State {
		case string(queue.StateCompleted):
			summary.Completed++
			switch row.Family {
			case "phrase":
				summary.PhraseOverlays++
			case "image":
				summary.ImageOverlays++
			}
			if row.ArtifactSHA256 != "" {
				hashes[row.ArtifactSHA256] = true
			}
			if row.LocalTiming != "" {
				summary.TimingSidecars++
			}
			if row.CompletedAt != nil {
				completed = append(completed, *row.CompletedAt)
			}
		case string(queue.StateFailed):
			summary.Failed++
			summary.Failures = append(summary.Failures, row.JobID+" (failed)")
		case string(queue.StateCancelled):
			summary.Cancelled++
			summary.Failures = append(summary.Failures, row.JobID+" (cancelled)")
		default:
			summary.NotTerminal++
			summary.Failures = append(summary.Failures, row.JobID+" ("+row.State+")")
		}
		if row.QueuedAt != nil {
			queued = append(queued, *row.QueuedAt)
		}
		if row.RenderWallMS != nil {
			renderWall = append(renderWall, float64(*row.RenderWallMS))
		}
		if row.QueueWaitMS != nil {
			queueWait = append(queueWait, float64(*row.QueueWaitMS))
		}
		if row.ChrononWallTimeMS != nil {
			chrononWall = append(chrononWall, *row.ChrononWallTimeMS)
		}
	}
	summary.DistinctHashes = len(hashes)
	summary.RenderWall = stats(renderWall)
	summary.QueueWait = stats(queueWait)
	summary.ChrononWall = stats(chrononWall)
	if len(queued) > 0 && len(completed) > 0 {
		wall := int64(completed[len(completed)-1].Sub(earliest(queued)).Milliseconds())
		summary.QueueWallMS = &wall
	}
	sort.Strings(summary.Failures)
	return summary
}

func stats(values []float64) *Stats {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var sum float64
	for _, value := range sorted {
		sum += value
	}
	return &Stats{
		Count:  len(sorted),
		MinMS:  sorted[0],
		P50MS:  sorted[len(sorted)/2],
		MaxMS:  sorted[len(sorted)-1],
		MeanMS: sum / float64(len(sorted)),
	}
}

func earliest(times []time.Time) time.Time {
	min := times[0]
	for _, value := range times[1:] {
		if value.Before(min) {
			min = value
		}
	}
	return min
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copied := value
	return &copied
}

func deltaMS(start, end time.Time) *int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	ms := end.Sub(start).Milliseconds()
	return &ms
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func printSummary(logger *log.Logger, report *Report) {
	summary := report.Summary
	logger.Printf("batch %s: %d/%d completed (%d phrase, %d image), %d timing sidecar(s)",
		report.BatchID, summary.Completed, summary.JobsTotal, summary.PhraseOverlays, summary.ImageOverlays, summary.TimingSidecars)
	if summary.QueueWallMS != nil {
		logger.Printf("queue wall: %d ms", *summary.QueueWallMS)
	}
	if summary.RenderWall != nil {
		logger.Printf("render wall per job: p50 %.0f ms, max %.0f ms", summary.RenderWall.P50MS, summary.RenderWall.MaxMS)
	}
	for _, failure := range summary.Failures {
		logger.Printf("FAILED %s", failure)
	}
}
