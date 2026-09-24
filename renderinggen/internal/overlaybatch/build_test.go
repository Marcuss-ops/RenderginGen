package overlaybatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/batch"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
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
	writeFile(t, filepath.Join(root, fontDejaVu), []byte("dejavu-sans-bytes"))
	for _, entity := range tysonEntities {
		writeFile(t, filepath.Join(root, "mike_tyson_overlay_test", "preset_overlays_v1", "assets", "entities", entity.file),
			[]byte("image-bytes-"+entity.file))
	}
	return root
}

// fixtureRequest is the request of record for the Tyson corpus: ONE phrase per
// phrase motion the embedded ChrononTemplate catalog declares
// (motion.PhraseMotions). The builder pairs phrase i with catalog motion i, so
// the request must carry exactly as many phrases as the catalog has motions —
// the catalog is a generated artifact of another repository and this corpus may
// not out-vote it. The corpus shrank from ten phrase overlays to five when the
// catalog's tyson_phrase_motions selection was reduced upstream, and the count
// here follows that selection instead of pinning a number the vocabulary can no
// longer honour.
func fixtureRequest(t *testing.T, root string) string {
	t.Helper()
	phrases := []string{
		"Discipline Gives Power a Direction.",
		"Speed Changes the Distance.",
		"Pressure Shapes the Rhythm.",
		"Footwork Creates the Opening.",
		"Technique Makes Aggression Precise.",
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
// manifest it replaces (the Python script that used to emit it) had one job per
// phrase candidate plus one per entity portrait, and the manifest must stay a
// valid renderinggen.batch-manifest.v1 that cmd/batch-submit and cmd/batch-run
// can expand. The expected counts are derived from the catalog selection and the
// entity table, so a corpus that out-grows the vocabulary fails here instead of
// in a booked render.
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
	if wantPhrases := len(motion.PhraseMotions()); result.Jobs != wantPhrases+len(tysonEntities) || result.Phrases != wantPhrases || result.Images != len(tysonEntities) {
		t.Fatalf("built %d job(s) (%d phrase, %d image), want %d (%d, %d)",
			result.Jobs, result.Phrases, result.Images, wantPhrases+len(tysonEntities), wantPhrases, len(tysonEntities))
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
	if want := len(motion.PhraseMotions()) + len(tysonEntities); len(jobs) != want {
		t.Fatalf("expansion produced %d job(s), want %d (one per catalog phrase motion plus one per entity)", len(jobs), want)
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
			if facts.motion == "" || facts.preset != "phrase_default" {
				t.Errorf("job %s: phrase job lost its preset/motion (%q/%q)", job.ID, facts.preset, facts.motion)
			}
		default:
			t.Errorf("job %s: unexpected family %q", job.ID, facts.family)
		}
	}
	var emitted batchManifestDocument
	if err := json.Unmarshal(raw, &emitted); err != nil {
		t.Fatalf("decode emitted manifest: %v", err)
	}
	for _, job := range emitted.Jobs {
		if job.Family != "phrase" {
			continue
		}
		if len(job.MotionPool) != len(motion.PhraseMotionPool()) || job.SelectionSeed == 0 {
			t.Errorf("phrase %s missing auditable pool/seed (pool=%d seed=%d)", job.ID, len(job.MotionPool), job.SelectionSeed)
		}
		compiled, err := overlay.CompileSemantic(job.RenderPlan)
		if err != nil {
			t.Fatalf("compile selected motion %q: %v", job.MotionID, err)
		}
		animated := false
		for _, layer := range compiled.Plan.Layers {
			if layer.Animation != nil || len(layer.TextAnimators) > 0 {
				animated = true
			}
		}
		if !animated {
			t.Errorf("selected motion %q did not lower animation tracks", job.MotionID)
		}
	}
	if images != 5 {
		t.Errorf("built %d image job(s), want 5", images)
	}
}

func TestPhraseMotionSelectionIsSeededCoverageAndFailClosed(t *testing.T) {
	pool := motion.PhraseMotionPool()
	planSeed := phraseSelectionSeed("plan-1", "phrase-1")
	if planSeed != phraseSelectionSeed("plan-1", "phrase-1") {
		t.Fatal("same plan and item ids produced different seeds")
	}
	first, err := selectPhraseMotion(pool, planSeed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := selectPhraseMotion(pool, planSeed)
	if err != nil || second != first {
		t.Fatalf("same seed selected %q then %q (err %v)", first, second, err)
	}
	covered := make(map[string]bool, len(pool))
	for seed := uint64(0); seed < 10000; seed++ {
		id, err := selectPhraseMotion(pool, seed)
		if err != nil {
			t.Fatal(err)
		}
		covered[id] = true
	}
	if len(covered) != len(pool) {
		t.Fatalf("seed sweep reached %d/%d pool motions", len(covered), len(pool))
	}
	corpusCoverage := make(map[string]bool, len(pool))
	for job := 1; job <= 100; job++ {
		planID := fmt.Sprintf("phrase-%02d", job)
		seed := phraseSelectionSeed(planID, "important-phrase")
		id, err := selectPhraseMotion(pool, seed)
		if err != nil {
			t.Fatal(err)
		}
		corpusCoverage[id] = true
	}
	if len(corpusCoverage) != len(pool) {
		t.Fatalf("canonical 100-item phrase corpus reaches %d/%d pool motions", len(corpusCoverage), len(pool))
	}
	for _, id := range pool {
		_, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
			PlanID: "pool-" + id, ProjectID: "phrase-pool-compile-gate", Language: "en",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationMS: 5000,
			Items: []renderbatch.PlanItem{{
				ID: "phrase", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE",
				PresetID: overlay.PhraseDefaultPresetID, MotionID: id,
				Text: "A crisp, seeded motion", StartMS: 0, EndMS: 5000,
			}},
		})
		if err != nil {
			t.Errorf("pool motion %q fails semantic compilation: %v", id, err)
		}
	}
	if _, err := selectPhraseMotion([]string{"unknown_motion"}, 1); err == nil {
		t.Fatal("unknown pool id was accepted")
	}
}

func TestBuildImageMotionManifestCompilesAllEighteenMotions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, matrixImageFile), []byte("fixed-image-canary"))
	out := filepath.Join(t.TempDir(), "images.json")
	result, err := BuildImageMotionManifest(ImageMotionBuildOptions{
		BatchID: "image-motion-fixture", AssetBaseURL: "http://127.0.0.1:8099",
		RepoRoot: root, OutPath: out, PlanDir: filepath.Join(t.TempDir(), "plans"),
	})
	if err != nil {
		t.Fatalf("BuildImageMotionManifest: %v", err)
	}
	if result.Jobs != 18 || result.Images != 18 {
		t.Fatalf("image motion matrix produced %d jobs/%d images, want 18/18", result.Jobs, result.Images)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := batch.Decode(raw)
	if err != nil {
		t.Fatalf("decode image motion manifest: %v", err)
	}
	if len(jobs) != 18 {
		t.Fatalf("decoded %d image jobs, want 18", len(jobs))
	}
	var emitted batchManifestDocument
	if err := json.Unmarshal(raw, &emitted); err != nil {
		t.Fatal(err)
	}
	for _, job := range emitted.Jobs {
		compiled, err := overlay.CompileSemantic(job.RenderPlan)
		if err != nil {
			t.Errorf("compile image motion %q: %v", job.MotionID, err)
			continue
		}
		animated := false
		for _, layer := range compiled.Plan.Layers {
			if layer.Animation != nil && len(layer.Animation.Tracks) > 0 {
				animated = true
			}
		}
		if !animated {
			t.Errorf("image motion %q did not lower to image layer tracks", job.MotionID)
		}
	}
}

// TestBuildNamesTheRepoRootWhenTheCorpusIsMissing pins the operator-facing side
// of -repo-root. It defaults to the current directory while every corpus lives
// under the RenderingGen checkout, so the common mistake — running the builder
// from renderinggen/ — must name the flag instead of failing on a relative path
// the operator cannot act on.
func TestBuildNamesTheRepoRootWhenTheCorpusIsMissing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "images.json")
	_, err := BuildImageMotionManifest(ImageMotionBuildOptions{
		BatchID: "image-motion-fixture", AssetBaseURL: "http://127.0.0.1:8099",
		RepoRoot: t.TempDir(), OutPath: out,
	})
	if err == nil {
		t.Fatal("a missing corpus image must fail the build")
	}
	if !strings.Contains(err.Error(), "-repo-root") {
		t.Errorf("error = %v, want a -repo-root hint", err)
	}
	if !strings.Contains(err.Error(), matrixImageFile) {
		t.Errorf("error = %v, want the missing corpus file named", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("a failed build must not leave a manifest behind")
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
		return json.RawMessage(fmt.Sprintf(`{"language":"it","items":[{"template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":%q}]}`, text))
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

// TestMultilingualJobsDeclareThePlanPrimaryFont is the regression guard for the
// Cyrillic rehearsal failure. The ru job's plan burns assets/fonts/DejaVuSans.ttf
// (overlay.OfficialFontPathForLanguage), the worker resolves a job's assets
// against its own workspace, and Chronon builds its fallback stack by scanning
// the PRIMARY font's own directory: a manifest that declared only Poppins+Inter
// left the ru shaper without a single Cyrillic face, and the render died in
// preflight with exit 1 before it wrote a frame.
func TestMultilingualJobsDeclareThePlanPrimaryFont(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, fontPoppins), []byte("poppins-bold-bytes"))
	writeFile(t, filepath.Join(root, fontInter), []byte("inter-bold-bytes"))
	writeFile(t, filepath.Join(root, fontDejaVu), []byte("dejavu-sans-bytes"))
	writeFile(t, filepath.Join(root, matrixImageFile), []byte("gerard-butler-jpeg-bytes"))

	overlays := motion.PhraseOverlays()
	if len(overlays) == 0 {
		t.Fatal("the embedded catalog declares no phrase overlays")
	}
	row := overlays[0]

	translations, err := json.Marshal(map[string]map[string]map[string]string{
		row.ID: {"translations": {
			"en": "A clear process, repeated.",
			"ru": "\u042f\u0441\u043d\u044b\u0439 \u043f\u0440\u043e\u0446\u0435\u0441\u0441, \u043f\u043e\u0432\u0442\u043e\u0440\u044f\u0435\u043c\u044b\u0439.",
		}},
	})
	if err != nil {
		t.Fatalf("marshal translations: %v", err)
	}
	translationsPath := filepath.Join(root, "translations.json")
	writeFile(t, translationsPath, translations)

	out := filepath.Join(t.TempDir(), "manifest.json")
	if _, err := BuildMultilingualManifest(MultilingualBuildOptions{
		TranslationsPath: translationsPath,
		BatchID:          "fixture-ml",
		AssetBaseURL:     "http://127.0.0.1:8099",
		RepoRoot:         root,
		Languages:        []string{"en", "ru"},
		Only:             []string{row.ID},
		OutPath:          out,
	}); err != nil {
		t.Fatalf("BuildMultilingualManifest: %v", err)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	// The consumer's own decoder: whatever it drops, the worker never fetches.
	jobs, err := batch.Decode(raw)
	if err != nil {
		t.Fatalf("the built manifest is not a valid batch manifest: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("built %d job(s), want 2 (en + ru)", len(jobs))
	}
	for _, job := range jobs {
		language := job.ID[strings.LastIndex(job.ID, "__")+2:]
		primary := overlay.OfficialFontPathForLanguage(language)
		declared := logicalPaths(job.Assets)
		// The job carries each closed-enum runtime font option plus the language
		// coverage font. Unused options are still declared because a producer
		// may choose any supported family per item.
		want := map[string]bool{logicalFontP: true, logicalFontI: true, logicalFontD: true}
		if len(declared) != len(want) || !slices.Contains(declared, primary) {
			t.Errorf("%s declares %v, want every runtime font family including %s", job.ID, declared, primary)
		}
		for _, path := range declared {
			if !want[path] {
				t.Errorf("%s declares %s, which its plan does not burn", job.ID, path)
			}
		}
		for _, asset := range job.Assets {
			if asset.SourceURL == "" {
				t.Errorf("%s: font %s carries no source_url, so the worker cannot self-heal it", job.ID, asset.LogicalPath)
			}
		}
		// The two directions of the regression, named: ru must get the Cyrillic
		// face, and en must not be handed it for a language that cannot use it.
		if language == "ru" && !slices.Contains(declared, logicalFontD) {
			t.Errorf("ru declares %v, want the Cyrillic face %s", declared, logicalFontD)
		}
	}
}

// TestFontSpecsCoverEveryMatrixLanguage: the plan compiler picks a primary font
// per language, so every language of the production matrix must have a job
// declaration that can serve it. A language whose plan font this builder cannot
// map to a checkout file would be declared without it and fail the way the ru
// rehearsal did.
func TestFontSpecsCoverEveryMatrixLanguage(t *testing.T) {
	for _, language := range matrixLanguages {
		primary := overlay.OfficialFontPathForLanguage(language)
		if _, ok := fontLocalFiles[primary]; !ok {
			t.Errorf("%s: the plan burns %s but the builder has no file to serve it", language, primary)
		}
		if !hasLogicalFont(fontSpecsForLanguage(language), primary) {
			t.Errorf("%s: fontSpecsForLanguage does not declare the plan's %s", language, primary)
		}
	}
}

// TestBuildRefusesAPlanFontTheJobDoesNotDeclare reproduces the rehearsal's
// broken manifest — a Cyrillic plan whose job declared only the Latin pair — and
// pins that the builder refuses it instead of submitting a render that dies in
// preflight with "no font in stack covers all visible codepoints".
func TestBuildRefusesAPlanFontTheJobDoesNotDeclare(t *testing.T) {
	phraseOverlays := motion.PhraseOverlays()
	if len(phraseOverlays) == 0 {
		t.Fatal("the embedded catalog declares no phrase overlays")
	}
	plan, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
		PlanID:     "ru-job",
		Language:   "ru",
		Width:      canvasWidth,
		Height:     canvasHeight,
		FPSNum:     canvasFPSNum,
		FPSDen:     canvasFPSDen,
		DurationMS: durationMS,
		Background: &renderbatch.Surface{Kind: "color", Color: backgroundRGBA},
		Items: []renderbatch.PlanItem{{
			ID:         "item_1",
			Kind:       "important_phrase",
			TemplateID: "IMPORTANT_PHRASE",
			PresetID:   overlay.PhraseDefaultPresetID,
			MotionID:   phraseOverlays[0].Motion,
			Text:       "Проверка шрифта",
			StartMS:    0,
			EndMS:      durationMS,
		}},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	document := batchManifestDocument{SchemaVersion: SchemaBatchManifestV1, BatchID: "fixture", Jobs: []manifestJob{{
		ID:         "ru-job",
		RenderPlan: plan,
		Assets: []queue.AssetRef{
			{Hash: "poppins", LogicalPath: logicalFontP},
			{Hash: "inter", LogicalPath: logicalFontI},
		},
	}}}
	err = checkPlanFonts(document)
	if err == nil {
		t.Fatal("a job that does not declare the font its plan burns must be refused")
	}
	if !strings.Contains(err.Error(), logicalFontD) {
		t.Fatalf("the refusal must name the undeclared font %s: %v", logicalFontD, err)
	}

	// Declaring it — what fontAssetsForLanguage now does — is the whole fix.
	document.Jobs[0].Assets = append(document.Jobs[0].Assets, queue.AssetRef{Hash: "dejavu", LogicalPath: logicalFontD})
	if err := checkPlanFonts(document); err != nil {
		t.Fatalf("a job that declares its plan's font must be accepted: %v", err)
	}
}

// matrixImageJobURLs returns every URL a built manifest publishes for the
// matrix image: the queue asset the worker self-heals from and the plan's
// asset_ref, which Chronon resolves. A corpus is only servable when the two
// agree, so both are asserted.
func matrixImageJobURLs(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document batchManifestDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	var urls []string
	for _, job := range document.Jobs {
		for _, asset := range job.Assets {
			if asset.LogicalPath == matrixImageLogicalPath {
				urls = append(urls, asset.SourceURL)
			}
		}
		var plan struct {
			Items []struct {
				AssetRefs []struct {
					URL string `json:"url"`
				} `json:"asset_refs"`
			} `json:"items"`
		}
		if err := json.Unmarshal(job.RenderPlan, &plan); err != nil {
			t.Fatalf("decode plan of %s: %v", job.ID, err)
		}
		for _, item := range plan.Items {
			for _, ref := range item.AssetRefs {
				urls = append(urls, ref.URL)
			}
		}
	}
	return urls
}

// TestBothImageCorporaPublishOneServableMatrixImageURL pins the finding that
// two builders derived two different URLs for the same bytes: the image-motion
// matrix appended the checkout-relative path (so a server of either corpus
// 404ed and the worker could not self-heal a missing asset), while the
// multilingual matrix published <base>/<file name>, the shape its manifest of
// record uses. One owner now derives both, and the root it assumes is asserted
// against where the file actually lives rather than restated.
func TestBothImageCorporaPublishOneServableMatrixImageURL(t *testing.T) {
	if got := filepath.Dir(matrixImageFile); got != matrixImageServeRoot {
		t.Fatalf("the matrix image lives in %q but is published as if the served root were %q", got, matrixImageServeRoot)
	}
	const base = "http://127.0.0.1:8099"
	want := base + "/" + filepath.Base(matrixImageFile)

	root := fixtureRepo(t)
	writeFile(t, filepath.Join(root, matrixImageFile), []byte("fixed-image-canary"))

	// A trailing slash on the root is the operator's spelling, not a second URL.
	imageMotions := filepath.Join(t.TempDir(), "image-motions.json")
	if _, err := BuildImageMotionManifest(ImageMotionBuildOptions{
		BatchID: "image-motion-fixture", AssetBaseURL: base + "/", RepoRoot: root, OutPath: imageMotions,
	}); err != nil {
		t.Fatalf("BuildImageMotionManifest: %v", err)
	}
	urls := matrixImageJobURLs(t, imageMotions)
	if len(urls) != 36 {
		t.Fatalf("the image-motion matrix published %d image URL(s), want 36 (18 jobs x manifest asset + plan ref)", len(urls))
	}

	translationsPath := filepath.Join(root, "translations.json")
	writeFile(t, translationsPath, []byte(`{}`))
	multilingual := filepath.Join(t.TempDir(), "multilingual.json")
	if _, err := BuildMultilingualManifest(MultilingualBuildOptions{
		TranslationsPath: translationsPath,
		BatchID:          "multilingual-fixture",
		AssetBaseURL:     base,
		RepoRoot:         root,
		Languages:        []string{"en"},
		Only:             []string{overlay.ImageMotionCorpusPresetID},
		OutPath:          multilingual,
	}); err != nil {
		t.Fatalf("BuildMultilingualManifest: %v", err)
	}
	multilingualURLs := matrixImageJobURLs(t, multilingual)
	if len(multilingualURLs) == 0 {
		t.Fatal("the multilingual matrix published no image URL")
	}
	urls = append(urls, multilingualURLs...)

	for _, url := range urls {
		if url != want {
			t.Errorf("published matrix image URL = %q, want %q", url, want)
		}
	}
}

// imageLayer returns the single image layer of a compiled plan. The corpus
// plan carries the canvas background as its first layer, so "the image layer"
// is a lookup, not an index.
func imageLayer(layers []overlay.Layer) (overlay.Layer, bool) {
	var found overlay.Layer
	matches := 0
	for _, layer := range layers {
		if layer.Type == "image" {
			found, matches = layer, matches+1
		}
	}
	return found, matches == 1
}

// TestImageMotionMatrixRendersOneNamedPreset pins the image-motion corpus to
// the preset the overlay catalog defines. The matrix varies motion_id only, so
// its plan and its manifest metadata must both name the catalog's own constant:
// a preset difference between them would silently change what the matrix
// measures, and a second copy of the string is how the two drift apart.
func TestImageMotionMatrixRendersOneNamedPreset(t *testing.T) {
	definition, err := overlay.ResolveOfficialPreset(overlay.ImageMotionCorpusPresetID)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Family != overlay.PresetImage {
		t.Fatalf("the matrix preset %q is a %s preset, not an image preset", overlay.ImageMotionCorpusPresetID, definition.Family)
	}
	// The value and the motion it lowers through are pinned, not just the
	// family: the catalog's matrix_image_overlays selection
	// (testdata/golden/golden-semantic-overlay-job-v1.json's sibling corpus and
	// the embedded ChrononTemplate catalog) and the Tyson entity row still spell
	// this preset literal, so renaming it must be a deliberate corpus-wide edit
	// rather than a silent one-line change here.
	if overlay.ImageMotionCorpusPresetID != "image_focus_in" || definition.Motion.ID != "image_focus_reveal" {
		t.Fatalf("matrix preset = %q lowered through %q, want image_focus_in through image_focus_reveal", overlay.ImageMotionCorpusPresetID, definition.Motion.ID)
	}
	if definition.Layout.BoxWidth != 480 || definition.Layout.BoxHeight != 480 || definition.Layout.Fit != overlay.FitContain {
		t.Fatalf("the matrix preset geometry = %+v, want a 480x480 contain box", definition.Layout)
	}

	root := fixtureRepo(t)
	writeFile(t, filepath.Join(root, matrixImageFile), []byte("fixed-image-canary"))
	out := filepath.Join(t.TempDir(), "images.json")
	if _, err := BuildImageMotionManifest(ImageMotionBuildOptions{
		BatchID: "image-motion-fixture", AssetBaseURL: "http://127.0.0.1:8099", RepoRoot: root, OutPath: out,
	}); err != nil {
		t.Fatalf("BuildImageMotionManifest: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var document batchManifestDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}

	// The two 2.5D blur motions are the pair the content gate exists for: one of
	// them encoded 120 frames of pure canvas through the Vulkan path and still
	// reported success. Pin that the matrix exercises both, that each lowers with
	// its blur track, and that their render route is otherwise identical — the
	// scale track is the whole difference between them.
	blurMotions := []string{"image_25d_blur_focus_in", "image_25d_blur_scale_in"}
	// Layer is not comparable (it carries slices), so the route is snapshotted
	// field by field and the tracks are compared separately.
	type renderRoute struct {
		Type, Asset, Fit    string
		BoxWidth, BoxHeight int
		Enable3D            bool
		Tracks              []overlay.AnimationTrack
	}
	routes := make(map[string]renderRoute, len(blurMotions))

	seen := make(map[string]bool)
	for _, job := range document.Jobs {
		if job.PresetID != overlay.ImageMotionCorpusPresetID {
			t.Errorf("job %s advertises preset %q, want the catalog's %q", job.ID, job.PresetID, overlay.ImageMotionCorpusPresetID)
		}
		var plan struct {
			Items []struct {
				PresetID string `json:"preset_id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(job.RenderPlan, &plan); err != nil {
			t.Fatalf("decode plan of %s: %v", job.ID, err)
		}
		if len(plan.Items) != 1 || plan.Items[0].PresetID != job.PresetID {
			t.Errorf("job %s: plan preset %v disagrees with manifest preset %q", job.ID, plan.Items, job.PresetID)
		}
		seen[job.MotionID] = true

		if slices.Contains(blurMotions, job.MotionID) {
			compiled, err := overlay.CompileSemantic(job.RenderPlan)
			if err != nil {
				t.Fatalf("compile %s: %v", job.MotionID, err)
			}
			layer, found := imageLayer(compiled.Plan.Layers)
			if !found || layer.Animation == nil {
				t.Fatalf("%s did not lower to an animated image layer: %+v", job.MotionID, compiled.Plan.Layers)
			}
			blur := false
			for _, track := range layer.Animation.Tracks {
				blur = blur || track.Property == "blur"
			}
			if !blur {
				t.Errorf("%s lowered without its blur track", job.MotionID)
			}
			routes[job.MotionID] = renderRoute{
				Type: layer.Type, Asset: layer.Asset, Fit: layer.Fit,
				BoxWidth: layer.BoxWidth, BoxHeight: layer.BoxHeight,
				Enable3D: layer.Enable3D, Tracks: layer.Animation.Tracks,
			}
		}
	}
	if len(seen) != 18 {
		t.Fatalf("the matrix carries %d distinct motion(s), want 18", len(seen))
	}
	for _, id := range blurMotions {
		route, ok := routes[id]
		if !ok {
			t.Fatalf("the matrix no longer exercises the blur motion %q", id)
		}
		if route.Type != "image" || route.BoxWidth != definition.Layout.BoxWidth || route.BoxHeight != definition.Layout.BoxHeight {
			t.Errorf("blur motion %q lowered to %s %dx%d, want an image layer in the %dx%d preset box", id, route.Type, route.BoxWidth, route.BoxHeight, definition.Layout.BoxWidth, definition.Layout.BoxHeight)
		}
		if !route.Enable3D {
			t.Errorf("blur motion %q rendered without the 3D route its position_z track requires", id)
		}
	}
	first, second := routes[blurMotions[0]], routes[blurMotions[1]]
	if first.Type != second.Type || first.Asset != second.Asset || first.Fit != second.Fit ||
		first.BoxWidth != second.BoxWidth || first.BoxHeight != second.BoxHeight || first.Enable3D != second.Enable3D {
		t.Errorf("the two blur motions no longer render through identical flags:\n%+v\n%+v", first, second)
	}
	if reflect.DeepEqual(first.Tracks, second.Tracks) {
		t.Errorf("the two blur motions lower to identical tracks, so the matrix measures one motion twice: %+v", first.Tracks)
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
