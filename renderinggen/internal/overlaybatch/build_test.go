package overlaybatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/batch"
)

// writeFile writes a fixture file, creating its directories.
func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fixtureRepo builds a checkout with the fonts and entity images the builders
// hash, and returns the fixed request of record for the Tyson corpus.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, fontPoppins), []byte("poppins-bold-bytes"))
	writeFile(t, filepath.Join(root, fontInter), []byte("inter-bold-bytes"))
	for _, entity := range tysonEntities {
		writeFile(t, filepath.Join(root, "mike_tyson_overlay_test", "preset_overlays_v1", "assets", "entities", entity.file),
			[]byte("image-bytes-"+entity.file))
	}
	return root
}

func fixtureRequest(t *testing.T, root string) string {
	t.Helper()
	phrases := []string{
		"Discipline Gives Power a Direction.",
		"Speed Changes the Distance.",
		"Pressure Shapes the Rhythm.",
		"Footwork Creates the Opening.",
		"Technique Makes Aggression Precise.",
		"Preparation Begins Long Before the Bell.",
		"A Loss Exposes What Victory Can Hide.",
		"A Champion Is More Than a Record.",
		"Reinvention Is Part of the Fight.",
		"Legacy Outlasts the Final Round.",
	}
	var segments []string
	for index, phrase := range phrases {
		segments = append(segments, fmt.Sprintf(`{"id":"segment-%02d","source_text":"Narration that carries the phrase: %s and more text."}`, index+1, phrase))
	}
	quoted := make([]string, 0, len(phrases))
	for _, phrase := range phrases {
		quoted = append(quoted, fmt.Sprintf("%q", phrase))
	}
	request := fmt.Sprintf(`{"version":1,"items":[{"script_params":{"segments":[%s]},"media_plan":{"extraction":{"important_phrases":[%s]}}}]}`,
		strings.Join(segments, ","), strings.Join(quoted, ","))
	path := filepath.Join(root, "request.json")
	writeFile(t, path, []byte(request))
	return path
}

// TestBuildTysonManifestProducesTheFullCorpus is the builder's contract test: the
// hand-written manifest it replaces (the Python script that used to emit it) had
// 15 jobs, and the manifest must stay a valid renderinggen.batch-manifest.v1 that
// cmd/batch-submit and cmd/batch-run can expand.
func TestBuildTysonManifestProducesTheFullCorpus(t *testing.T) {
	root := fixtureRepo(t)
	request := fixtureRequest(t, root)
	out := filepath.Join(t.TempDir(), "manifest.json")

	result, err := BuildTysonManifest(TysonBuildOptions{
		RequestPath:  request,
		BatchID:      "fixture-batch",
		AssetBaseURL: "http://127.0.0.1:8099",
		RepoRoot:     root,
		OutPath:      out,
		PlanDir:      filepath.Join(t.TempDir(), "plans"),
	})
	if err != nil {
		t.Fatalf("BuildTysonManifest: %v", err)
	}
	if result.Jobs != 15 || result.Phrases != 10 || result.Images != 5 {
		t.Fatalf("built %d job(s) (%d phrase, %d image), want 15 (10, 5)", result.Jobs, result.Phrases, result.Images)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	// The shared expansion is the submission contract: the built document must
	// decode through it, with content-derived job ids and idempotency keys.
	jobs, err := batch.Decode(raw)
	if err != nil {
		t.Fatalf("the built manifest is not a valid batch manifest: %v", err)
	}
	if len(jobs) != 15 {
		t.Fatalf("expansion produced %d job(s), want 15", len(jobs))
	}
	for _, job := range jobs {
		if !strings.HasPrefix(job.ID, "fixture-batch:") {
			t.Errorf("job %q is not scoped by the batch id", job.ID)
		}
		if job.IdempotencyKey == "" {
			t.Errorf("job %q carries no idempotency key: a replay would render twice", job.ID)
		}
	}

	probe, err := decodeProbe(raw)
	if err != nil {
		t.Fatalf("decodeProbe: %v", err)
	}
	images := 0
	for _, job := range probe.Jobs {
		facts := job.facts()
		switch facts.family {
		case "image":
			images++
			if facts.entranceF == nil || *facts.entranceF != imageEntranceFrames {
				t.Errorf("job %s: entrance frames = %v, want %d", job.ID, facts.entranceF, imageEntranceFrames)
			}
			if facts.entranceS == nil || *facts.entranceS < 1.5 {
				t.Errorf("job %s: entrance seconds = %v, want >= 1.5", job.ID, facts.entranceS)
			}
			if facts.asset == "" || facts.attribution == "" {
				t.Errorf("job %s: image provenance is incomplete (asset=%q attribution=%q)", job.ID, facts.asset, facts.attribution)
			}
		case "phrase":
			if facts.motion == "" || facts.preset != "apple_v2" {
				t.Errorf("job %s: phrase job lost its preset/motion (%q/%q)", job.ID, facts.preset, facts.motion)
			}
		default:
			t.Errorf("job %s: unexpected family %q", job.ID, facts.family)
		}
	}
	if images != 5 {
		t.Errorf("built %d image job(s), want 5", images)
	}
}

// TestBuildTysonManifestRejectsAStalePhrase keeps the request↔manifest coupling
// honest: a phrase that is no longer in its source segment is a stale request,
// not something to render.
func TestBuildTysonManifestRejectsAStalePhrase(t *testing.T) {
	root := fixtureRepo(t)
	request := fixtureRequest(t, root)
	raw, err := os.ReadFile(request)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	// Blank the first segment's source text so its phrase no longer appears.
	patched := strings.Replace(string(raw), "Narration that carries the phrase: Discipline Gives Power a Direction.",
		"Unrelated narration.", 1)
	writeFile(t, request, []byte(patched))

	_, err = BuildTysonManifest(TysonBuildOptions{
		RequestPath:  request,
		BatchID:      "fixture-batch",
		AssetBaseURL: "http://127.0.0.1:8099",
		RepoRoot:     root,
		OutPath:      filepath.Join(t.TempDir(), "manifest.json"),
	})
	if err == nil {
		t.Fatal("a phrase that is not in its source segment must fail the build")
	}
	if !strings.Contains(err.Error(), "is not in its source segment") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBuildWithoutAssetBaseURLFails: the worker fetches a corpus through the
// manifest's source_url, so a build that cannot name one is a build that would
// render with missing assets.
func TestBuildWithoutAssetBaseURLFails(t *testing.T) {
	root := fixtureRepo(t)
	if _, err := BuildTysonManifest(TysonBuildOptions{
		RequestPath: fixtureRequest(t, root),
		BatchID:     "fixture-batch",
		RepoRoot:    root,
		OutPath:     filepath.Join(t.TempDir(), "manifest.json"),
	}); err == nil {
		t.Fatal("-asset-base-url must be required")
	}
}

// TestDecodeProbeRejectsJobsWithoutIDs: the report claims to describe every job,
// so an anonymous job is a hard error rather than a nameless row.
func TestDecodeProbeRejectsJobsWithoutIDs(t *testing.T) {
	if _, err := decodeProbe([]byte(`{"schema_version":"renderinggen.batch-manifest.v1","batch_id":"b","jobs":[{"render_plan":{}}]}`)); err == nil {
		t.Fatal("a job without an id must fail decoding")
	}
	if _, err := decodeProbe([]byte(`{"schema_version":"renderinggen.batch-manifest.v1","jobs":[]}`)); err == nil {
		t.Fatal("a manifest without a batch_id must fail decoding")
	}
}

// TestFamilyAndFolderMapping pins the reporting labels and the corpus layout.
func TestFamilyAndFolderMapping(t *testing.T) {
	cases := []struct{ kind, template, family, folder string }{
		{"entity_image", "IMAGE_OVERLAY", "image", "images"},
		{"important_phrase", "IMPORTANT_PHRASE", "phrase", "phrases"},
		{"", "IMAGE_OVERLAY", "image", "images"},
		{"", "IMPORTANT_PHRASE", "phrase", "phrases"},
	}
	for _, testCase := range cases {
		family := familyOf(testCase.kind, testCase.template)
		if family != testCase.family {
			t.Errorf("familyOf(%q, %q) = %q, want %q", testCase.kind, testCase.template, family, testCase.family)
		}
		if folder := downloadFolder(family); folder != testCase.folder {
			t.Errorf("downloadFolder(%q) = %q, want %q", family, folder, testCase.folder)
		}
	}
}

// TestArtifactFileName: a downloaded render names the batch it came from.
func TestArtifactFileName(t *testing.T) {
	if got := artifactFileName("tyson-overlays-v3:image-01"); got != "tyson-overlays-v3-image-01" {
		t.Fatalf("artifactFileName = %q", got)
	}
}

// TestDistinctnessSeesACollision: the check exists to catch one language's bytes
// answering for another language's text.
func TestDistinctnessSeesACollision(t *testing.T) {
	plan := func(text string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"language":"it","items":[{"template_id":"IMPORTANT_PHRASE","preset_id":"apple_v2","text":%q}]}`, text))
	}
	probe := &manifestProbe{
		BatchID: "b",
		Jobs: []reportProbe{
			{ID: "phrase_01__it", RenderPlan: plan("ciao")},
			{ID: "phrase_01__en", RenderPlan: plan("hello")},
			{ID: "phrase_01__de", RenderPlan: plan("hello")},
		},
	}
	rows := distinctness(probe, map[string]string{
		"phrase_01__it": "hash-a",
		"phrase_01__en": "hash-b",
		"phrase_01__de": "hash-b", // same bytes as en, different text: legitimate
	})
	if len(rows) != 1 || len(rows[0].Collisions) != 0 {
		t.Fatalf("identical text with identical bytes must not be a collision: %+v", rows)
	}
	rows = distinctness(probe, map[string]string{
		"phrase_01__it": "hash-a",
		"phrase_01__en": "hash-a", // same bytes, different text: a real collision
		"phrase_01__de": "hash-b",
	})
	if len(rows) != 1 || len(rows[0].Collisions) == 0 {
		t.Fatalf("different text with identical bytes must be reported: %+v", rows)
	}
	if rows[0].DistinctHash != 2 {
		t.Errorf("distinct hashes = %d, want 2", rows[0].DistinctHash)
	}
}

// TestParseHexColor: the ink check must read the canvas colour it counts against.
func TestParseHexColor(t *testing.T) {
	got, err := ParseHexColor("#EEF1E7")
	if err != nil {
		t.Fatalf("ParseHexColor: %v", err)
	}
	if got != [3]uint8{238, 241, 231} {
		t.Fatalf("ParseHexColor = %v, want [238 241 231]", got)
	}
	if _, err := ParseHexColor("EEF1E7"); err == nil {
		t.Fatal("a colour without # must be rejected")
	}
}
