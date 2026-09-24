package overlaybatch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// verifyCanvasBackground is the corpus canvas colour the fixtures paint, so
// "non-canvas pixel" means the same thing in the fixture and in production.
var verifyCanvasBackground = [3]uint8{238, 241, 231}

// verifyImageClip renders the fixture the image-family gate measures: the bare
// canvas, or the canvas with the canonical 480x480 card drawn from the middle of
// the clip onwards.
//
// The blank clip is the shape the observed failure produced — 120 structurally
// perfect frames of pure canvas — so the gate is tested against the incident
// rather than against a synthetic "empty file".
func verifyImageClip(t *testing.T, card bool) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "color=c=0xEEF1E7:s=1920x1080:r=24:d=5"}
	if card {
		args = append(args, "-vf",
			"drawbox=x=(iw-480)/2:y=(ih-480)/2:w=480:h=480:color=0x1E5AA8:t=fill:enable='gte(t,0.5)'")
	}
	path := filepath.Join(t.TempDir(), "clip.mp4")
	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "24", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not build the fixture: %v\n%s", err, out)
	}
	return path
}

// verifyImageBatch runs a whole Verify pass over one image-family job whose
// artifact is clip, served by a fake queue and a fake artifact host.
//
// It goes through Verify (not verifyOne) on purpose: the content problem has to
// reach the VERDICT, which is what a run is judged by.
func verifyImageBatch(t *testing.T, clip string, opts VerifyOptions) (*VerifyReport, error) {
	t.Helper()
	digest, err := sha256File(clip)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(clip)
	if err != nil {
		t.Fatal(err)
	}
	const clipFrames = canvasFPSNum * 5
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/jobs/"):
			job := queue.Job{
				ID:    "image-motion-01",
				State: queue.StateCompleted,
				Artifact: &queue.Artifact{
					ArtifactURL:  baseURL + "/artifact.mp4",
					ArtifactHash: digest,
					SizeBytes:    info.Size(),
					Width:        canvasWidth,
					Height:       canvasHeight,
					FPSNum:       canvasFPSNum,
					FPSDen:       canvasFPSDen,
					FrameCount:   clipFrames,
					DurationUS:   5_000_000,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(job)
		case r.URL.Path == "/artifact.mp4":
			http.ServeFile(w, r, clip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	dir := t.TempDir()
	// The artifact contract is derived from the plan, so the fixture states its
	// duration the way a real manifest does.
	doc := `{"schema_version":"` + SchemaBatchManifestV1 + `","batch_id":"content-gate","jobs":[` +
		`{"id":"image-motion-01","family":"image","motion_id":"image_25d_blur_focus_in",` +
		`"render_plan":{"duration_ms":5000,"items":[{"id":"image-motion","start_ms":0,"end_ms":5000}]}}]}`
	manifest := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifest, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	opts.ManifestPath = manifest
	opts.QueueURL = srv.URL
	opts.ReportPath = filepath.Join(dir, "report.json")
	opts.StageDir = filepath.Join(dir, "stage")
	if opts.Background == [3]uint8{} {
		opts.Background = verifyCanvasBackground
	}
	return Verify(context.Background(), opts)
}

// TestImageContentGateFailsAPureCanvasRender is the regression the image gate
// exists for. The Vulkan 2.5D blur motions encoded 120 frames of pure canvas;
// every structural check, the content-address check and the distinctness check
// passed them, and the batch reported success. The verdict must now be FAIL, and
// the problem must name the content rather than sending a reader to the encoder.
func TestImageContentGateFailsAPureCanvasRender(t *testing.T) {
	report, err := verifyImageBatch(t, verifyImageClip(t, false), VerifyOptions{})
	if err == nil {
		t.Fatal("a pure-canvas image artifact must fail verification")
	}
	if report == nil || report.Verdict != "FAIL" {
		t.Fatalf("report = %+v, want verdict FAIL", report)
	}
	if len(report.StructuralFailed) != 1 {
		t.Fatalf("structural failures = %d, want 1", len(report.StructuralFailed))
	}
	problems := strings.Join(report.StructuralFailed[0].Problems, "; ")
	if !strings.Contains(problems, "content_missing") {
		t.Fatalf("problems = %q, want the content failure named", problems)
	}
	row := report.Rows[0]
	if row.Ink == nil || *row.Ink != 0 {
		t.Fatalf("measured ink = %v, want 0 for a pure canvas", row.Ink)
	}
	if want := imageContentFrames(canvasFPSNum * 5); !reflect.DeepEqual(row.SampledFrames, want) {
		t.Fatalf("sampled frames = %v, want %v", row.SampledFrames, want)
	}
}

// TestImageContentGatePassesARenderedCard is the other half: the gate must be
// silent about a legitimate render, or it becomes noise an operator disables.
func TestImageContentGatePassesARenderedCard(t *testing.T) {
	report, err := verifyImageBatch(t, verifyImageClip(t, true), VerifyOptions{})
	if err != nil {
		t.Fatalf("a rendered image card must pass: %v", err)
	}
	if report.Verdict != "PASS" {
		t.Fatalf("verdict = %s, want PASS", report.Verdict)
	}
	// The least inked sampled frame carries the whole 480x480 card.
	if ink := report.Rows[0].Ink; ink == nil || *ink != 480*480 {
		t.Fatalf("measured ink = %v, want %d", ink, 480*480)
	}
}

// TestImageContentGateIsDisableable pins the documented escape hatch: a negative
// floor turns the family's content assertion off without touching the structural
// contract, which is what an operator measuring something else needs.
func TestImageContentGateIsDisableable(t *testing.T) {
	report, err := verifyImageBatch(t, verifyImageClip(t, false), VerifyOptions{ImageInkFloor: -1})
	if err != nil {
		t.Fatalf("a disabled content gate must not fail the batch: %v", err)
	}
	if report.Verdict != "PASS" {
		t.Fatalf("verdict = %s, want PASS", report.Verdict)
	}
	if row := report.Rows[0]; len(row.SampledFrames) != 0 || row.Ink != nil {
		t.Fatalf("a disabled gate must not report a measurement: %+v", row)
	}
}

// TestContentCheckCoversEveryEmittedFamily ties the gate's family switch to what
// the builders actually write. The previous gate compared against the literal
// "phrase", so a second family could only ever be added by editing the one
// branch that mentioned the first — and the image family silently had no pixel
// check at all for as long as that was true.
func TestContentCheckCoversEveryEmittedFamily(t *testing.T) {
	opts := VerifyOptions{InkFloor: DefaultInkFloor, ImageInkFloor: DefaultImageInkFloor, InkFrame: 60}
	if got := contentCheckFor(opts, familyPhrase, 120); !got.phraseBand || got.floor != DefaultInkFloor ||
		!reflect.DeepEqual(got.frames, []int{60}) {
		t.Fatalf("phrase content check = %+v", got)
	}
	if got := contentCheckFor(opts, familyImage, 120); got.phraseBand || got.floor != DefaultImageInkFloor ||
		!reflect.DeepEqual(got.frames, []int{48, 84, 108}) {
		t.Fatalf("image content check = %+v", got)
	}
	// Both families must resolve to an assertion: a family the switch does not
	// know still gets its structural checks, but silently loses the pixel proof.
	for _, family := range []string{familyPhrase, familyImage} {
		if check := contentCheckFor(opts, family, 120); len(check.frames) == 0 || check.floor <= 0 {
			t.Errorf("family %q resolves to no content assertion: %+v", family, check)
		}
	}
	if err := checkEmittedFamiliesMatchTheGate(t); err != nil {
		t.Fatal(err)
	}
}

// checkEmittedFamiliesMatchTheGate builds every corpus the content switch must
// know and reports the first family the builders emit but the gate does not
// cover.
//
// This is the pin that makes the family vocabulary a decision instead of a
// coincidence: the builders write "phrase"/"image", the gate switches on
// familyPhrase/familyImage, and a third corpus family added without a gate case
// fails here rather than shipping as a batch that reports PASS without ever
// looking at a pixel.
func checkEmittedFamiliesMatchTheGate(t *testing.T) error {
	t.Helper()
	imageRoot := t.TempDir()
	writeFile(t, filepath.Join(imageRoot, matrixImageFile), []byte("fixed-image-canary"))
	imageOut := filepath.Join(t.TempDir(), "images.json")
	if _, err := BuildImageMotionManifest(ImageMotionBuildOptions{
		BatchID: "family-pin-image", AssetBaseURL: "http://127.0.0.1:8099",
		RepoRoot: imageRoot, OutPath: imageOut,
	}); err != nil {
		return err
	}
	repo := fixtureRepo(t)
	tysonOut := filepath.Join(t.TempDir(), "tyson.json")
	if _, err := BuildTysonManifest(TysonBuildOptions{
		RequestPath: fixtureRequest(t, repo), BatchID: "family-pin-phrase",
		AssetBaseURL: "http://127.0.0.1:8099", RepoRoot: repo, OutPath: tysonOut,
	}); err != nil {
		return err
	}

	known := make(map[string]bool, len(emittedManifestFamilies))
	for _, family := range emittedManifestFamilies {
		known[family] = true
	}
	for _, outPath := range []string{imageOut, tysonOut} {
		raw, err := os.ReadFile(outPath)
		if err != nil {
			return err
		}
		probe, err := decodeProbe(raw)
		if err != nil {
			return err
		}
		for _, job := range probe.Jobs {
			if family := job.facts().family; !known[family] {
				return errUncoveredFamily{family}
			}
		}
	}
	return nil
}

type errUncoveredFamily struct{ family string }

func (e errUncoveredFamily) Error() string {
	return "overlaybatch: the builders emit family " + e.family + ", which the content gate does not know"
}

// emittedManifestFamilies is the family vocabulary the corpus builders write. It
// is declared next to the gate so a new corpus family is a visible decision
// rather than a job that silently skips its content check.
var emittedManifestFamilies = []string{familyPhrase, familyImage}
