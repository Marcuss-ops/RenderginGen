package overlaybatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// VerifyReportSchema identifies the verification report.
const VerifyReportSchema = "renderinggen.batch-verify-report.v1"

// DefaultInkFloor is the minimum number of glyph pixels a phrase band must carry.
//
// A phrase that rendered produces tens of thousands of ink pixels; the floor
// exists to catch the opposite failure, an artifact that reports "completed"
// while rasterising almost nothing because the shaper had no font covering the
// text. The measured missing-glyph run produced 790 px for a phrase that needs
// ~28,000.
const DefaultInkFloor = 5000

// inkTolerance is the per-channel distance that still counts as background.
const inkTolerance = 24

// VerifyOptions configures one verification pass.
type VerifyOptions struct {
	// ManifestPath is the batch manifest the batch was submitted from.
	ManifestPath string
	QueueURL     string
	ReportPath   string
	// StageDir caches the downloaded artifacts.
	StageDir string
	// RequireBackend asserts the certified backend of every artifact. Empty
	// disables the check.
	RequireBackend string
	// InkFloor is the minimum ink pixels for a phrase overlay. Zero uses
	// DefaultInkFloor; a negative value disables the pixel check.
	InkFloor int
	// InkFrame is the frame the pixel check samples.
	InkFrame int
	// Background is the corpus canvas colour the ink check counts against.
	Background [3]uint8
	// BaselineManifestPath, when set, is a previous run of the same overlays
	// whose artifacts are compared against this one (bytes changed, ink grew).
	BaselineManifestPath string
	// BaselineDir caches the baseline downloads.
	BaselineDir string
	Logger      *log.Logger
}

// VerifyRow is one job's verification result.
type VerifyRow struct {
	JobID    string   `json:"job_id"`
	Family   string   `json:"family,omitempty"`
	Language string   `json:"language,omitempty"`
	Text     string   `json:"text,omitempty"`
	State    string   `json:"state"`
	SHA256   string   `json:"sha256,omitempty"`
	Bytes    int64    `json:"bytes,omitempty"`
	Ink      *int     `json:"ink_pixels,omitempty"`
	Problems []string `json:"problems,omitempty"`
}

// DistinctnessRow summarizes one overlay's language variants.
type DistinctnessRow struct {
	Overlay      string   `json:"overlay"`
	Languages    int      `json:"languages"`
	DistinctHash int      `json:"distinct_hashes"`
	Collisions   []string `json:"collisions,omitempty"`
}

// BaselineRow compares one overlay between two runs.
type BaselineRow struct {
	JobID        string `json:"job_id"`
	Language     string `json:"language,omitempty"`
	BaselineInk  *int   `json:"baseline_ink_pixels,omitempty"`
	CurrentInk   *int   `json:"current_ink_pixels,omitempty"`
	BytesChanged bool   `json:"bytes_changed"`
	InkIncreased bool   `json:"ink_increased"`
	RendersText  bool   `json:"renders_text"`
	Note         string `json:"note,omitempty"`
}

// VerifyReport is the verification document of record.
type VerifyReport struct {
	Schema           string            `json:"schema"`
	BatchID          string            `json:"batch_id"`
	GeneratedAt      time.Time         `json:"generated_at"`
	Manifest         string            `json:"manifest"`
	Queue            string            `json:"queue"`
	Jobs             int               `json:"jobs"`
	Passed           int               `json:"passed"`
	StructuralFailed []VerifyRow       `json:"structural_failures,omitempty"`
	Distinctness     []DistinctnessRow `json:"distinctness"`
	Baseline         []BaselineRow     `json:"baseline,omitempty"`
	Rows             []VerifyRow       `json:"rows"`
	Verdict          string            `json:"verdict"`
}

// Verify certifies every job of a batch against its artifact contract, its
// rendered pixels, and (optionally) the previous run of the same overlays.
//
// It fails closed: a job that is not `completed`, an artifact whose structural
// facts differ from the manifest, bytes that do not hash to their content
// address, or a phrase band without glyph ink are all FAIL.
func Verify(ctx context.Context, opts VerifyOptions) (*VerifyReport, error) {
	if opts.ManifestPath == "" {
		return nil, fmt.Errorf("overlaybatch: -manifest is required")
	}
	if opts.ReportPath == "" {
		return nil, fmt.Errorf("overlaybatch: -report is required")
	}
	if opts.QueueURL == "" {
		opts.QueueURL = "http://localhost:8081"
	}
	if opts.InkFloor == 0 {
		opts.InkFloor = DefaultInkFloor
	}
	if opts.InkFrame <= 0 {
		opts.InkFrame = 60
	}
	if opts.Background == [3]uint8{} {
		opts.Background = [3]uint8{238, 241, 231}
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "batch-verify: ", log.LstdFlags)
	}

	raw, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("overlaybatch: read manifest: %w", err)
	}
	probe, err := decodeProbe(raw)
	if err != nil {
		return nil, err
	}
	client := queue.New(opts.QueueURL)
	httpClient := &http.Client{Timeout: 10 * time.Minute}

	durations := planDurations(probe)
	report := &VerifyReport{
		Schema:      VerifyReportSchema,
		BatchID:     probe.BatchID,
		GeneratedAt: time.Now().UTC(),
		Manifest:    opts.ManifestPath,
		Queue:       opts.QueueURL,
		Jobs:        len(probe.Jobs),
	}

	hashes := make(map[string]string, len(probe.Jobs))
	for _, job := range probe.Jobs {
		facts := job.facts()
		queueID := probe.BatchID + ":" + job.ID
		row, err := verifyOne(ctx, client, httpClient, opts, queueID, job.ID, facts, durations[job.ID])
		if err != nil {
			return nil, err
		}
		if row.SHA256 != "" {
			hashes[job.ID] = row.SHA256
		}
		if len(row.Problems) > 0 {
			report.StructuralFailed = append(report.StructuralFailed, row)
		} else {
			report.Passed++
		}
		report.Rows = append(report.Rows, row)
	}

	report.Distinctness = distinctness(probe, hashes)

	if opts.BaselineManifestPath != "" {
		baseline, err := baselineComparison(ctx, client, httpClient, opts, probe, report.Rows)
		if err != nil {
			return nil, err
		}
		report.Baseline = baseline
	}

	failed := len(report.StructuralFailed) > 0
	for _, row := range report.Distinctness {
		if len(row.Collisions) > 0 {
			failed = true
		}
	}
	for _, row := range report.Baseline {
		if !row.RendersText || !row.InkIncreased {
			failed = true
		}
	}
	report.Verdict = "PASS"
	if failed {
		report.Verdict = "FAIL"
	}
	if err := writeJSON(opts.ReportPath, report); err != nil {
		return nil, err
	}

	logger.Printf("structural: %d/%d PASS", report.Passed, report.Jobs)
	for index, row := range report.StructuralFailed {
		if index == 10 {
			logger.Printf("... %d more structural failure(s)", len(report.StructuralFailed)-index)
			break
		}
		logger.Printf("  FAIL %s: %s", row.JobID, strings.Join(row.Problems, "; "))
	}
	for _, row := range report.Baseline {
		logger.Printf("baseline %s: ink %s -> %s, renders_text=%v ink_increased=%v bytes_changed=%v",
			row.JobID, inkText(row.BaselineInk), inkText(row.CurrentInk), row.RendersText, row.InkIncreased, row.BytesChanged)
	}
	logger.Printf("VERDICT: %s", report.Verdict)
	if failed {
		return report, fmt.Errorf("overlaybatch: verification failed (%d structural, %d collision, %d baseline)",
			len(report.StructuralFailed), collisionCount(report.Distinctness), len(report.Baseline))
	}
	return report, nil
}

// verifyOne checks one job's certified facts and, for phrase overlays, that the
// translated text actually rasterised.
func verifyOne(ctx context.Context, client *queue.Client, httpClient *http.Client, opts VerifyOptions,
	queueID, logicalID string, facts jobFacts, duration int64) (VerifyRow, error) {

	row := VerifyRow{JobID: queueID, Family: facts.family, Language: facts.language, Text: facts.text}
	body, err := client.Get(ctx, queueID)
	if err != nil {
		return row, fmt.Errorf("overlaybatch: get %s: %w", queueID, err)
	}
	row.State = string(body.State)
	artifact := body.Artifact
	if body.State != queue.StateCompleted {
		row.Problems = append(row.Problems, "state="+row.State)
		return row, nil
	}
	if artifact == nil {
		row.Problems = append(row.Problems, "no artifact")
		return row, nil
	}
	expectDurationUS := duration * 1000
	for _, check := range []struct {
		name string
		got  any
		want any
	}{
		{"width", artifact.Width, canvasWidth},
		{"height", artifact.Height, canvasHeight},
		{"fps_num", artifact.FPSNum, canvasFPSNum},
		{"fps_den", artifact.FPSDen, canvasFPSDen},
	} {
		if check.got != check.want {
			row.Problems = append(row.Problems, fmt.Sprintf("%s=%v want %v", check.name, check.got, check.want))
		}
	}
	if artifact.DurationUS != expectDurationUS {
		row.Problems = append(row.Problems, fmt.Sprintf("duration_us=%d want %d", artifact.DurationUS, expectDurationUS))
	}
	if wantFrames := int(duration * canvasFPSNum / (1000 * canvasFPSDen)); artifact.FrameCount != wantFrames {
		row.Problems = append(row.Problems, fmt.Sprintf("frame_count=%d want %d", artifact.FrameCount, wantFrames))
	}
	if opts.RequireBackend != "" && artifact.Backend != opts.RequireBackend {
		row.Problems = append(row.Problems, fmt.Sprintf("backend=%q want %q", artifact.Backend, opts.RequireBackend))
	}
	if artifact.ArtifactURL == "" {
		row.Problems = append(row.Problems, "artifact has no URL")
		return row, nil
	}

	local := filepath.Join(opts.StageDir, logicalID+".mp4")
	if err := download(httpClient, artifact.ArtifactURL, local, artifact.ArtifactHash); err != nil {
		row.Problems = append(row.Problems, "download: "+err.Error())
		return row, nil
	}
	row.SHA256 = artifact.ArtifactHash
	row.Bytes = artifact.SizeBytes

	if opts.InkFloor < 0 || facts.family != "phrase" {
		return row, nil
	}
	ink, err := inkPixels(ctx, local, opts.InkFrame, artifact.Width, artifact.Height, opts.Background)
	if err != nil {
		row.Problems = append(row.Problems, "ink check: "+err.Error())
		return row, nil
	}
	row.Ink = &ink
	if ink < opts.InkFloor {
		row.Problems = append(row.Problems, fmt.Sprintf("ink_pixels=%d below floor %d", ink, opts.InkFloor))
	}
	return row, nil
}

// baselineComparison replays the same overlays from a previous manifest and
// records whether this run changed the bytes and rasterised more text.
//
// The criterion is "the current artifact renders its text": when the baseline
// has no artifact at all (it failed), the pair-wise increase is not applicable
// rather than a failure, and only the ink floor decides.
func baselineComparison(ctx context.Context, client *queue.Client, httpClient *http.Client,
	opts VerifyOptions, probe *manifestProbe, rows []VerifyRow) ([]BaselineRow, error) {

	raw, err := os.ReadFile(opts.BaselineManifestPath)
	if err != nil {
		return nil, fmt.Errorf("overlaybatch: read baseline manifest: %w", err)
	}
	baseline, err := decodeProbe(raw)
	if err != nil {
		return nil, err
	}
	currentInk := make(map[string]*int, len(rows))
	currentHash := make(map[string]string, len(rows))
	currentState := make(map[string]string, len(rows))
	for _, row := range rows {
		logical := strings.TrimPrefix(row.JobID, probe.BatchID+":")
		currentInk[logical] = row.Ink
		currentHash[logical] = row.SHA256
		currentState[logical] = row.State
	}

	baselineInk := map[string]*int{}
	baselineHash := map[string]string{}
	for _, job := range baseline.Jobs {
		facts := job.facts()
		if facts.family != "phrase" {
			continue
		}
		queueID := baseline.BatchID + ":" + job.ID
		body, err := client.Get(ctx, queueID)
		if err != nil {
			// A baseline job that cannot be read is recorded as absent, never as
			// a zero that would look like a measurement.
			baselineInk[job.ID] = nil
			continue
		}
		if body.State != queue.StateCompleted || body.Artifact == nil || body.Artifact.ArtifactURL == "" {
			baselineInk[job.ID] = nil
			continue
		}
		local := filepath.Join(opts.BaselineDir, job.ID+".mp4")
		if err := download(httpClient, body.Artifact.ArtifactURL, local, body.Artifact.ArtifactHash); err != nil {
			return nil, fmt.Errorf("overlaybatch: download baseline %s: %w", job.ID, err)
		}
		baselineHash[job.ID] = body.Artifact.ArtifactHash
		if opts.InkFloor < 0 {
			continue
		}
		ink, err := inkPixels(ctx, local, opts.InkFrame, body.Artifact.Width, body.Artifact.Height, opts.Background)
		if err != nil {
			return nil, fmt.Errorf("overlaybatch: baseline ink %s: %w", job.ID, err)
		}
		value := ink
		baselineInk[job.ID] = &value
	}

	common := make([]string, 0, len(baselineInk))
	for logical := range baselineInk {
		if _, ok := currentHash[logical]; ok {
			common = append(common, logical)
		}
	}
	sort.Strings(common)

	out := make([]BaselineRow, 0, len(common))
	for _, logical := range common {
		var facts jobFacts
		for _, job := range baseline.Jobs {
			if job.ID == logical {
				facts = job.facts()
				break
			}
		}
		entry := BaselineRow{
			JobID:       logical,
			Language:    facts.language,
			BaselineInk: baselineInk[logical],
			CurrentInk:  currentInk[logical],
		}
		entry.BytesChanged = baselineHash[logical] != "" && currentHash[logical] != "" &&
			baselineHash[logical] != currentHash[logical]
		entry.RendersText = currentInk[logical] != nil && *currentInk[logical] >= opts.InkFloor
		if entry.BaselineInk == nil {
			// No baseline artifact: nothing to compare against. The note keeps
			// that visible instead of implying an increase of zero.
			entry.InkIncreased = true
			entry.Note = "baseline has no artifact (state=" + currentState[logical] + "); comparison not applicable"
		} else {
			entry.InkIncreased = entry.CurrentInk != nil && *entry.CurrentInk > *entry.BaselineInk
		}
		out = append(out, entry)
	}
	return out, nil
}

// distinctness verifies that one overlay rendered in two languages whose
// translated text differs produced different bytes. Identical text may
// legitimately produce identical bytes, so only differing text is compared.
func distinctness(probe *manifestProbe, hashes map[string]string) []DistinctnessRow {
	byOverlay := map[string][]reportProbe{}
	for _, job := range probe.Jobs {
		key := job.ID
		if index := strings.LastIndex(key, "__"); index > 0 {
			key = key[:index]
		}
		byOverlay[key] = append(byOverlay[key], job)
	}
	keys := make([]string, 0, len(byOverlay))
	for key := range byOverlay {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]DistinctnessRow, 0, len(keys))
	for _, key := range keys {
		jobs := byOverlay[key]
		texts := map[string]string{}
		digests := map[string]string{}
		for _, job := range jobs {
			facts := job.facts()
			texts[job.ID] = facts.text
			digests[job.ID] = hashes[job.ID]
		}
		ids := make([]string, 0, len(jobs))
		for id := range texts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		row := DistinctnessRow{Overlay: key, Languages: len(ids)}
		unique := map[string]bool{}
		for _, id := range ids {
			if digests[id] != "" {
				unique[digests[id]] = true
			}
		}
		row.DistinctHash = len(unique)
		for i, left := range ids {
			for _, right := range ids[i+1:] {
				if texts[left] == texts[right] {
					continue
				}
				if digests[left] != "" && digests[left] == digests[right] {
					row.Collisions = append(row.Collisions,
						fmt.Sprintf("%s==%s identical bytes for different text", left, right))
				}
			}
		}
		out = append(out, row)
	}
	return out
}

// inkPixels counts the pixels of one video frame that differ from the canvas
// colour inside the phrase band (the middle 120 rows the canonical preset
// anchors). One frame is enough: the preset animates opacity and scale, not
// position.
func inkPixels(ctx context.Context, video string, frame, width, height int, background [3]uint8) (int, error) {
	if width <= 0 || height <= 0 {
		return 0, fmt.Errorf("frame geometry unknown (%dx%d)", width, height)
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", video,
		"-vf", fmt.Sprintf("select=eq(n\\,%d)", frame),
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg frame %d: %w: %s", frame, err, strings.TrimSpace(stderr.String()))
	}
	expected := width * height * 3
	if len(raw) < expected {
		return 0, fmt.Errorf("ffmpeg returned %d bytes, want %d", len(raw), expected)
	}
	top := height/2 - 60
	bottom := height/2 + 60
	if top < 0 {
		top = 0
	}
	if bottom > height {
		bottom = height
	}
	ink := 0
	for y := top; y < bottom; y++ {
		rowStart := y * width * 3
		for x := 0; x < width; x++ {
			offset := rowStart + x*3
			if differs(raw[offset], background[0]) || differs(raw[offset+1], background[1]) || differs(raw[offset+2], background[2]) {
				ink++
			}
		}
	}
	return ink, nil
}

func differs(value, background uint8) bool {
	delta := int(value) - int(background)
	if delta < 0 {
		delta = -delta
	}
	return delta > inkTolerance
}

// planDurations recovers each job's item duration, so the artifact contract is
// derived from the plan instead of being restated per corpus.
func planDurations(probe *manifestProbe) map[string]int64 {
	out := make(map[string]int64, len(probe.Jobs))
	for _, job := range probe.Jobs {
		var plan struct {
			DurationMS int64 `json:"duration_ms"`
			Items      []struct {
				StartMS int64 `json:"start_ms"`
				EndMS   int64 `json:"end_ms"`
			} `json:"items"`
		}
		if json.Unmarshal(job.RenderPlan, &plan) != nil {
			continue
		}
		duration := plan.DurationMS
		for _, item := range plan.Items {
			if end := item.EndMS - item.StartMS; end > duration {
				duration = end
			}
		}
		if duration > 0 {
			out[job.ID] = duration
		}
	}
	return out
}

// ParseHexColor parses "#RRGGBB" into its three channels. The leading '#' is
// required so a bare six-digit string cannot be mistaken for something else.
func ParseHexColor(value string) ([3]uint8, error) {
	var out [3]uint8
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "#") || len(trimmed) != 7 {
		return out, fmt.Errorf("colour %q must be #RRGGBB", value)
	}
	trimmed = trimmed[1:]
	for index := 0; index < 3; index++ {
		channel, err := strconv.ParseUint(trimmed[index*2:index*2+2], 16, 8)
		if err != nil {
			return out, fmt.Errorf("colour %q: %w", value, err)
		}
		out[index] = uint8(channel)
	}
	return out, nil
}

func collisionCount(rows []DistinctnessRow) int {
	total := 0
	for _, row := range rows {
		total += len(row.Collisions)
	}
	return total
}

func inkText(value *int) string {
	if value == nil {
		return "n/a"
	}
	return strconv.Itoa(*value)
}
