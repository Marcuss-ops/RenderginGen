// Package renderbatch runs a matrix of overlay preset renders described by a
// data manifest.
//
// Why it exists. The matrix used to be twenty Go struct literals inside
// cmd/batch-render-presets, and the semantic plan for each one was assembled
// with fmt.Sprintf over a JSON template: adding a preset meant editing the
// command, a mistyped field produced a document the worker rejected at render
// time (after GPU time was already committed), and the job text, ID, motion and
// output path were three parallel lists that had to stay aligned by hand.
//
// Here the matrix is data (a versioned manifest), the plan is built from typed
// structs and marshaled by encoding/json, and the WHOLE matrix is compiled up
// front: over the same overlay.CompileSemantic entry point the worker uses, so a
// plan error is a load error naming the job, not a mid-run failure on job 17.
package renderbatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// SchemaVersion is the manifest contract this package decodes. It is checked
// exactly: a future format must be named differently rather than partly
// understood.
const SchemaVersion = "renderinggen.preset-render-manifest.v1"

// OverlayPlanSchema is the semantic contract the built plans declare.
const OverlayPlanSchema = "renderinggen.overlay-plan.v1"

// DefaultDurationMS is applied when a job omits duration_ms.
const DefaultDurationMS = 5000

// Manifest is the whole render matrix.
type Manifest struct {
	SchemaVersion string   `json:"schema_version"`
	Canvas        Canvas   `json:"canvas"`
	Background    *Surface `json:"background,omitempty"`
	// OutputRoot is the directory every job's Output is relative to. The
	// --out-dir flag overrides it; the current working directory is used when
	// both are empty. There is deliberately no repository-root discovery: the
	// previous command walked parent directories looking for a folder named
	// "RenderingGen", which silently depended on the checkout layout.
	OutputRoot string `json:"output_root,omitempty"`
	// AssetsRoot is where the renderer resolves the plan's relative asset
	// references. These plans carry no assets (they are text over a colour), so
	// it only has to be a readable directory.
	AssetsRoot string `json:"assets_root,omitempty"`
	Jobs       []Job  `json:"jobs"`
}

// Canvas is the output contract every job renders at.
type Canvas struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	FPSNum int `json:"fps_num"`
	FPSDen int `json:"fps_den"`
}

// Surface is the plan's typed background block. Kind "color" is the only one
// this batch uses, and Color is the RGBA quad the compiler requires.
type Surface struct {
	Kind  string    `json:"kind"`
	Color []float64 `json:"color,omitempty"`
}

// Job is one preset render.
type Job struct {
	ID         string `json:"id"`
	TemplateID string `json:"template_id"`
	PresetID   string `json:"preset_id"`
	MotionID   string `json:"motion_id,omitempty"`
	Text       string `json:"text,omitempty"`
	// DurationMS defaults to DefaultDurationMS.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// Output is the rendered file, relative to the effective output root.
	Output string `json:"output"`
}

// Expect is the media contract a rendered job is verified against. Every field
// is DERIVED from the manifest (canvas + duration), never restated: the previous
// command hardcoded "120 frames at 1920x1080" at the verification call site,
// which silently stopped being true for any job with another duration.
type Expect struct {
	Frames int
	Width  int
	Height int
	FPSNum int
	FPSDen int
}

// Expectation derives the media contract for a job on this canvas.
func (j Job) Expectation(canvas Canvas) Expect {
	duration := j.DurationMS
	if duration <= 0 {
		duration = DefaultDurationMS
	}
	return Expect{
		Frames: int(duration * int64(canvas.FPSNum) / (1000 * int64(canvas.FPSDen))),
		Width:  canvas.Width,
		Height: canvas.Height,
		FPSNum: canvas.FPSNum,
		FPSDen: canvas.FPSDen,
	}
}

// PreparedJob is one job with its plan built, compiled and its paths resolved.
type PreparedJob struct {
	Job        Job
	Expect     Expect
	PlanPath   string
	OutputPath string
	// Plan is the exact document written to PlanPath.
	Plan []byte
	// PlanDigest is the plan's content address, so two runs that differ only in
	// the plan can be told apart in the log.
	PlanDigest string
}

// Decode reads and validates a manifest document. Decoding is strict: an
// unknown key is a producer bug (usually a rename) and fails here rather than
// being dropped on the floor.
func Decode(raw []byte) (*Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("renderbatch: decode manifest: %w", err)
	}
	if err := manifest.validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (m *Manifest) validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("renderbatch: schema_version must be %q, got %q", SchemaVersion, m.SchemaVersion)
	}
	if m.Canvas.Width <= 0 || m.Canvas.Height <= 0 {
		return fmt.Errorf("renderbatch: canvas width/height must be positive, got %dx%d", m.Canvas.Width, m.Canvas.Height)
	}
	if m.Canvas.FPSNum <= 0 || m.Canvas.FPSDen <= 0 {
		return fmt.Errorf("renderbatch: canvas fps must be positive, got %d/%d", m.Canvas.FPSNum, m.Canvas.FPSDen)
	}
	if len(m.Jobs) == 0 {
		return fmt.Errorf("renderbatch: manifest declares no jobs")
	}
	background, err := m.background()
	if err != nil {
		return err
	}
	m.Background = background

	seenID := make(map[string]bool, len(m.Jobs))
	seenOutput := make(map[string]string, len(m.Jobs))
	var problems []string
	for i, job := range m.Jobs {
		switch {
		case job.ID == "":
			problems = append(problems, fmt.Sprintf("job %d: id is required", i+1))
		case seenID[job.ID]:
			problems = append(problems, fmt.Sprintf("job %q: id is declared twice", job.ID))
		}
		seenID[job.ID] = true
		if job.TemplateID == "" {
			problems = append(problems, fmt.Sprintf("job %q: template_id is required", job.ID))
		}
		if job.DurationMS < 0 {
			problems = append(problems, fmt.Sprintf("job %q: duration_ms must not be negative", job.ID))
		}
		if _, err := cleanRelative(job.Output); err != nil {
			problems = append(problems, fmt.Sprintf("job %q: output: %v", job.ID, err))
			continue
		}
		// Two jobs writing one file would make the second result depend on the
		// order the pool happened to run them in.
		if owner, ok := seenOutput[job.Output]; ok {
			problems = append(problems, fmt.Sprintf("job %q: output %q is already written by job %q", job.ID, job.Output, owner))
		}
		seenOutput[job.Output] = job.ID

		if expect := job.Expectation(m.Canvas); expect.Frames <= 0 {
			problems = append(problems, fmt.Sprintf("job %q: duration_ms %d is shorter than one frame at %d/%d fps", job.ID, job.DurationMS, m.Canvas.FPSNum, m.Canvas.FPSDen))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("renderbatch: invalid manifest:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// background returns the manifest's background, defaulting to the neutral
// canvas colour the preset matrix was authored against. The default is declared
// here (once) instead of being repeated in every job.
func (m *Manifest) background() (*Surface, error) {
	bg := m.Background
	if bg == nil {
		bg = &Surface{Kind: "color", Color: []float64{0.9333333333333333, 0.9450980392156862, 0.9058823529411765, 1.0}}
	}
	if strings.ToLower(strings.TrimSpace(bg.Kind)) != "color" {
		return nil, fmt.Errorf("renderbatch: background kind must be %q, got %q", "color", bg.Kind)
	}
	if len(bg.Color) != 4 {
		return nil, fmt.Errorf("renderbatch: background color must be RGBA[4], got %d components", len(bg.Color))
	}
	return bg, nil
}

// Prepare resolves every job's paths and builds AND COMPILES its plan.
//
// Compiling up front is the point: overlay.CompileSemantic is the same lowering
// the worker runs, so a template/preset/motion typo anywhere in the matrix is
// reported here — with the job id — before a single frame is rendered. The
// previous command discovered such errors inside the worker goroutine and called
// log.Fatalf, killing the process (and the warm daemons it owned) mid-batch.
func (m *Manifest) Prepare(outputRoot string) ([]PreparedJob, error) {
	if outputRoot == "" {
		outputRoot = m.OutputRoot
	}
	prepared := make([]PreparedJob, 0, len(m.Jobs))
	var problems []string
	for _, job := range m.Jobs {
		plan, err := m.buildPlan(job)
		if err != nil {
			problems = append(problems, fmt.Sprintf("job %q: %v", job.ID, err))
			continue
		}
		outputRel, err := cleanRelative(job.Output)
		if err != nil {
			problems = append(problems, fmt.Sprintf("job %q: output: %v", job.ID, err))
			continue
		}
		outputPath := outputRel
		if outputRoot != "" {
			outputPath = filepath.Join(outputRoot, filepath.FromSlash(outputRel))
		}
		// plan_path is not a manifest field: deriving it beside the output keeps
		// exactly one place that decides where a render's artefacts live.
		planPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "_plan.json"
		digest := sha256.Sum256(plan)
		prepared = append(prepared, PreparedJob{
			Job:        job,
			Expect:     job.Expectation(m.Canvas),
			PlanPath:   planPath,
			OutputPath: outputPath,
			Plan:       plan,
			PlanDigest: hex.EncodeToString(digest[:]),
		})
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("renderbatch: %d job(s) do not compile:\n  - %s", len(problems), strings.Join(problems, "\n  - "))
	}
	return prepared, nil
}

// buildPlan builds one job's semantic plan through the shared writer.
func (m *Manifest) buildPlan(job Job) ([]byte, error) {
	duration := job.DurationMS
	if duration <= 0 {
		duration = DefaultDurationMS
	}
	return BuildPlan(PlanSpec{
		PlanID:     job.ID,
		Width:      m.Canvas.Width,
		Height:     m.Canvas.Height,
		FPSNum:     m.Canvas.FPSNum,
		FPSDen:     m.Canvas.FPSDen,
		DurationMS: duration,
		Background: &Surface{Kind: m.Background.Kind, Color: m.Background.Color},
		Items: []PlanItem{{
			ID:         "item_1",
			TemplateID: job.TemplateID,
			PresetID:   job.PresetID,
			MotionID:   job.MotionID,
			Text:       job.Text,
			StartMS:    0,
			EndMS:      duration,
		}},
	})
}

// Roots are the resolved directories a batch reads from and writes to.
type Roots struct {
	Output string
	Assets string
}

// ResolveRoots resolves the output and asset roots.
//
// A manifest's own relative paths resolve against the MANIFEST'S directory, not
// the working directory, so a batch is reproducible from anywhere: the previous
// command instead walked parent directories looking for a folder named
// "RenderingGen" and silently depended on the checkout layout. A command-line
// override is taken as given (i.e. relative to the caller's working directory),
// which is what an operator typing a path expects.
func (m *Manifest) ResolveRoots(manifestPath, outputFlag, assetsFlag string) (Roots, error) {
	manifestDir, err := filepath.Abs(filepath.Dir(manifestPath))
	if err != nil {
		return Roots{}, fmt.Errorf("resolve manifest directory: %w", err)
	}
	output := filepath.Join(manifestDir, filepath.FromSlash(m.OutputRoot))
	if outputFlag != "" {
		output, err = filepath.Abs(outputFlag)
		if err != nil {
			return Roots{}, fmt.Errorf("resolve -out-dir: %w", err)
		}
	}
	assets := filepath.Join(manifestDir, filepath.FromSlash(m.AssetsRoot))
	if assetsFlag != "" {
		assets, err = filepath.Abs(assetsFlag)
		if err != nil {
			return Roots{}, fmt.Errorf("resolve -assets-root: %w", err)
		}
	}
	return Roots{Output: output, Assets: assets}, nil
}

// cleanRelative validates a manifest-declared relative path. Absolute paths and
// parent escapes are rejected: the manifest is data, and a data file must not be
// able to write outside the output root.
func cleanRelative(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	slashed := filepath.ToSlash(rel)
	if path.IsAbs(slashed) || filepath.IsAbs(rel) || strings.HasPrefix(slashed, "../") || slashed == ".." {
		return "", fmt.Errorf("path %q must be relative to the output root", rel)
	}
	cleaned := path.Clean(slashed)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path %q escapes the output root", rel)
	}
	return cleaned, nil
}
