package renderbatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shippedManifestPath is the matrix the batch command renders. It is loaded by
// the tests so a manifest edit that stops compiling fails the unit gate rather
// than the first real run.
const shippedManifestPath = "../../cmd/batch-render-presets/preset-render-manifest.json"

func minimalManifest(t *testing.T, jobs string) []byte {
	t.Helper()
	return []byte(`{
	  "schema_version": "` + SchemaVersion + `",
	  "canvas": {"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1},
	  "jobs": [` + jobs + `]
	}`)
}

const oneJob = `{"id":"job-1","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","motion_id":"kinetic_split_word","text":"HELLO","duration_ms":5000,"output":"out/one.mp4"}`

// TestDecodeIsStrictAndValidated pins the load-time contract: an unknown key, a
// wrong schema, a bad canvas and structurally broken jobs are all rejected
// before anything renders.
func TestDecodeIsStrictAndValidated(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"unknown key", `{"schema_version":"` + SchemaVersion + `","canvas":{"width":1,"height":1,"fps_num":1,"fps_den":1},"jobs":[],"surprise":1}`},
		{"wrong schema", `{"schema_version":"something.else","canvas":{"width":1,"height":1,"fps_num":1,"fps_den":1},"jobs":[` + oneJob + `]}`},
		{"no jobs", `{"schema_version":"` + SchemaVersion + `","canvas":{"width":1,"height":1,"fps_num":1,"fps_den":1},"jobs":[]}`},
		{"bad canvas", `{"schema_version":"` + SchemaVersion + `","canvas":{"width":0,"height":1,"fps_num":1,"fps_den":1},"jobs":[` + oneJob + `]}`},
		{"bad fps", `{"schema_version":"` + SchemaVersion + `","canvas":{"width":1,"height":1,"fps_num":24,"fps_den":0},"jobs":[` + oneJob + `]}`},
		{"missing id", string(minimalManifest(t, `{"template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","output":"a.mp4"}`))},
		{"missing template", string(minimalManifest(t, `{"id":"a","preset_id":"apple_v2","output":"a.mp4"}`))},
		{"duplicate id", string(minimalManifest(t, oneJob+`,`+strings.Replace(oneJob, "out/one.mp4", "out/two.mp4", 1)))},
		{"duplicate output", string(minimalManifest(t, oneJob+`,`+strings.Replace(oneJob, `"job-1"`, `"job-2"`, 1)))},
		{"absolute output", string(minimalManifest(t, `{"id":"a","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","output":"/etc/escape.mp4"}`))},
		{"escaping output", string(minimalManifest(t, `{"id":"a","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","output":"../escape.mp4"}`))},
		{"negative duration", string(minimalManifest(t, `{"id":"a","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","duration_ms":-1,"output":"a.mp4"}`))},
		{"duration shorter than a frame", string(minimalManifest(t, `{"id":"a","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","duration_ms":1,"output":"a.mp4"}`))},
		{"non-colour background", `{"schema_version":"` + SchemaVersion + `","canvas":{"width":1,"height":1,"fps_num":1,"fps_den":1},"background":{"kind":"video"},"jobs":[` + oneJob + `]}`},
		{"background colour not RGBA", `{"schema_version":"` + SchemaVersion + `","canvas":{"width":1,"height":1,"fps_num":1,"fps_den":1},"background":{"kind":"color","color":[1,1,1]},"jobs":[` + oneJob + `]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode([]byte(tc.raw)); err == nil {
				t.Fatalf("expected a decode error for %s", tc.name)
			}
		})
	}
}

// TestDecodeAcceptsAMinimalManifest is the counterweight to the rejection table:
// the shape a caller actually writes must load.
func TestDecodeAcceptsAMinimalManifest(t *testing.T) {
	manifest, err := Decode(minimalManifest(t, oneJob))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if manifest.Background == nil {
		t.Fatal("the neutral default background must be filled in")
	}
	if len(manifest.Background.Color) != 4 {
		t.Fatalf("default colour = %v, want RGBA[4]", manifest.Background.Color)
	}
}

// TestExpectationIsDerivedNotRestated pins that the verification contract comes
// from the canvas and the duration: 5 s at 24 fps is 120 frames, and a different
// duration yields a different expectation instead of the hardcoded 120 the old
// command always asserted.
func TestExpectationIsDerivedNotRestated(t *testing.T) {
	canvas := Canvas{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1}
	job := Job{ID: "a", DurationMS: 5000}
	if got := job.Expectation(canvas); got.Frames != 120 || got.Width != 1920 || got.Height != 1080 {
		t.Fatalf("expectation = %+v, want 120 frames at 1920x1080", got)
	}
	job.DurationMS = 2500
	if got := job.Expectation(canvas); got.Frames != 60 {
		t.Fatalf("expectation for 2.5s = %d frames, want 60", got.Frames)
	}
	// An omitted duration takes the manifest default.
	job.DurationMS = 0
	if got := job.Expectation(canvas); got.Frames != DefaultDurationMS*24/1000 {
		t.Fatalf("default expectation = %d frames", got.Frames)
	}
}

// TestPrepareCompilesTheWholeMatrix pins the fail-fast contract: a job whose
// preset cannot resolve is reported by id at prepare time, so no earlier job is
// rendered first.
func TestPrepareCompilesTheWholeMatrix(t *testing.T) {
	manifest, err := Decode(minimalManifest(t,
		`{"id":"good","template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","text":"HELLO","duration_ms":2000,"output":"out/good.mp4"},`+
			`{"id":"broken","template_id":"IMPORTANT_PHRASE","preset_id":"no_such_preset","text":"HELLO","duration_ms":2000,"output":"out/broken.mp4"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	_, err = manifest.Prepare(t.TempDir())
	if err == nil {
		t.Fatal("a job with an unresolvable preset must fail preparation")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error %q must name the failing job", err)
	}
	if strings.Contains(err.Error(), "good") {
		t.Fatalf("error %q must not implicate the job that compiles", err)
	}
}

// TestPrepareDerivesPaths pins where a render's artefacts live: the plan sits
// beside its output with a _plan suffix, and outputs are relative to the
// effective root.
func TestPrepareDerivesPaths(t *testing.T) {
	manifest, err := Decode(minimalManifest(t, oneJob))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	root := t.TempDir()
	prepared, err := manifest.Prepare(root)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(prepared) != 1 {
		t.Fatalf("prepared %d jobs, want 1", len(prepared))
	}
	wantOutput := filepath.Join(root, "out", "one.mp4")
	if prepared[0].OutputPath != wantOutput {
		t.Errorf("output = %q, want %q", prepared[0].OutputPath, wantOutput)
	}
	if wantPlan := filepath.Join(root, "out", "one_plan.json"); prepared[0].PlanPath != wantPlan {
		t.Errorf("plan = %q, want %q", prepared[0].PlanPath, wantPlan)
	}
	if len(prepared[0].PlanDigest) != 64 {
		t.Errorf("plan digest = %q, want a sha256 hex digest", prepared[0].PlanDigest)
	}
	// The plan must be the semantic document the worker compiles, not a concrete
	// render plan: RenderingGen owns the lowering.
	if !strings.Contains(string(prepared[0].Plan), OverlayPlanSchema) {
		t.Errorf("plan does not declare %s: %s", OverlayPlanSchema, prepared[0].Plan)
	}
} // TestResolveRootsIsManifestRelative pins that a manifest's relative roots
// resolve against the manifest's own directory (so the batch is reproducible
// from any working directory), that a flag override wins, and that a manifest
// without roots resolves to its own directory rather than to the process cwd.
func TestResolveRootsIsManifestRelative(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "nested", "manifest.json")

	manifest := &Manifest{OutputRoot: "out", AssetsRoot: "assets"}
	roots, err := manifest.ResolveRoots(manifestPath, "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantOutput := filepath.Join(dir, "nested", "out")
	wantAssets := filepath.Join(dir, "nested", "assets")
	if roots.Output != wantOutput {
		t.Errorf("output root = %q, want %q (relative to the manifest)", roots.Output, wantOutput)
	}
	if roots.Assets != wantAssets {
		t.Errorf("assets root = %q, want %q (relative to the manifest)", roots.Assets, wantAssets)
	}

	override := filepath.Join(dir, "elsewhere")
	roots, err = manifest.ResolveRoots(manifestPath, override, override)
	if err != nil {
		t.Fatalf("resolve with overrides: %v", err)
	}
	if roots.Output != override || roots.Assets != override {
		t.Errorf("explicit roots = %q/%q, want the overrides", roots.Output, roots.Assets)
	}

	// No declared roots: the manifest's own directory, not the process cwd.
	roots, err = (&Manifest{}).ResolveRoots(manifestPath, "", "")
	if err != nil {
		t.Fatalf("resolve defaults: %v", err)
	}
	if roots.Output != filepath.Join(dir, "nested") {
		t.Errorf("default output root = %q, want the manifest directory", roots.Output)
	}
}

// TestPhraseAnimationMatrixIsOneMotionPerPhrase pins the published phrase
// corpus (phrase_animations_v1): the ten editorial phrase candidates, each with
// its OWN motion, all rendering at the canvas that was published. The corpus is
// reproducible only from this manifest — the rendered mp4s are gitignored — so
// a duplicate motion (two phrases showing the same effect) or a job that stops
// compiling has to fail here rather than at the next real run.
func TestPhraseAnimationMatrixIsOneMotionPerPhrase(t *testing.T) {
	const path = "../../cmd/batch-render-presets/phrase-animations-v1-manifest.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read phrase animation manifest: %v", err)
	}
	manifest, err := Decode(raw)
	if err != nil {
		t.Fatalf("decode phrase animation manifest: %v", err)
	}
	if len(manifest.Jobs) != 10 {
		t.Fatalf("phrase animation manifest declares %d jobs, want the 10 phrase candidates", len(manifest.Jobs))
	}
	prepared, err := manifest.Prepare(t.TempDir())
	if err != nil {
		t.Fatalf("phrase animation manifest does not compile: %v", err)
	}
	const wantFrames = 120 // 5000 ms at 24/1 fps
	motions := make(map[string]string, len(prepared))
	for _, p := range prepared {
		if p.Expect.Frames != wantFrames {
			t.Errorf("job %s expects %d frames, want %d", p.Job.ID, p.Expect.Frames, wantFrames)
		}
		if p.Job.PresetID != "apple_v2" {
			t.Errorf("job %s preset = %q, want apple_v2", p.Job.ID, p.Job.PresetID)
		}
		if p.Job.Text == "" {
			t.Errorf("job %s carries no phrase text", p.Job.ID)
		}
		// The published artifacts live in one corpus directory whose names are
		// the Drive publication names, so the output path is part of the contract.
		if !strings.HasPrefix(p.Job.Output, "phrase_animations_v1/") {
			t.Errorf("job %s output = %q, want it under phrase_animations_v1/", p.Job.ID, p.Job.Output)
		}
		if owner, ok := motions[p.Job.MotionID]; ok {
			t.Errorf("motion %s is used by both %s and %s: each phrase needs its own effect", p.Job.MotionID, owner, p.Job.ID)
			continue
		}
		motions[p.Job.MotionID] = p.Job.ID
	}
	if len(motions) != len(prepared) {
		t.Errorf("%d distinct motion(s) across %d jobs", len(motions), len(prepared))
	}
}

// TestShippedMatrixCompiles is the guard on the real matrix: every job in the
// manifest this command ships must decode AND lower through the worker's own
// compiler. It is what makes a preset rename or a dropped motion a test failure
// instead of a runtime surprise.
func TestShippedMatrixCompiles(t *testing.T) {
	raw, err := os.ReadFile(shippedManifestPath)
	if err != nil {
		t.Fatalf("read shipped manifest: %v", err)
	}
	manifest, err := Decode(raw)
	if err != nil {
		t.Fatalf("decode shipped manifest: %v", err)
	}
	if len(manifest.Jobs) != 20 {
		t.Fatalf("shipped manifest declares %d jobs, want the 20-preset matrix", len(manifest.Jobs))
	}
	prepared, err := manifest.Prepare(t.TempDir())
	if err != nil {
		t.Fatalf("shipped manifest does not compile: %v", err)
	}
	if len(prepared) != len(manifest.Jobs) {
		t.Fatalf("prepared %d of %d jobs", len(prepared), len(manifest.Jobs))
	}
	const wantFrames = 120 // 5000 ms at 24/1 fps
	for _, p := range prepared {
		if p.Expect.Frames != wantFrames {
			t.Errorf("job %s expects %d frames, want %d", p.Job.ID, p.Expect.Frames, wantFrames)
		}
		if p.Job.PresetID != "apple_v2" {
			t.Errorf("job %s preset = %q", p.Job.ID, p.Job.PresetID)
		}
		// The default background must reach every plan as a colour surface.
		if !strings.Contains(string(p.Plan), `"kind":"color"`) {
			t.Errorf("job %s plan carries no colour background: %s", p.Job.ID, p.Plan)
		}
	}
}
