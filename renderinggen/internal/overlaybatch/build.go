package overlaybatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

// Corpus canvas constants. Both preset corpora render the same 5-second
// 1920x1080/24 fps clip on the pale olive canvas the preset matrix was authored
// against, so their clips are directly comparable.
const (
	canvasWidth  = 1920
	canvasHeight = 1080
	canvasFPSNum = 24
	canvasFPSDen = 1
	durationMS   = 5000

	// SchemaBatchManifestV1 is the batch master this package emits: the same
	// envelope cmd/batch-submit expands.
	SchemaBatchManifestV1 = "renderinggen.batch-manifest.v1"

	fontPoppins  = "testdata/golden/assets/fonts/Poppins-Bold.ttf"
	fontInter    = "testdata/golden/assets/fonts/Inter-Bold.ttf"
	logicalFontP = "assets/fonts/Poppins-Bold.ttf"
	logicalFontI = "assets/fonts/Inter-Bold.ttf"
)

// backgroundRGBA is the corpus canvas colour (hex #EEF1E7).
var backgroundRGBA = []float64{0.9333333333333333, 0.9450980392156862, 0.9058823529411765, 1.0}

// BuildResult reports what a build wrote.
type BuildResult struct {
	BatchID string
	Jobs    int
	Phrases int
	Images  int
	OutPath string
	PlanDir string
}

// manifestJob is one job of the emitted manifest: the submission fields plus the
// reporting metadata the run report re-reads. internal/batch ignores the extra
// fields, so this single document is both the input of record and the report
// source.
type manifestJob struct {
	ID         string           `json:"id"`
	RenderPlan json.RawMessage  `json:"render_plan"`
	Assets     []queue.AssetRef `json:"assets"`

	Family                  string   `json:"family,omitempty"`
	Text                    string   `json:"text,omitempty"`
	MotionID                string   `json:"motion_id,omitempty"`
	PresetID                string   `json:"preset_id,omitempty"`
	EntityID                string   `json:"entity_id,omitempty"`
	EntityName              string   `json:"entity_name,omitempty"`
	Asset                   string   `json:"asset,omitempty"`
	AssetSource             string   `json:"asset_source,omitempty"`
	AssetAttribution        string   `json:"asset_attribution,omitempty"`
	EntranceDurationFrames  *int     `json:"entrance_duration_frames,omitempty"`
	EntranceDurationSeconds *float64 `json:"entrance_duration_seconds,omitempty"`
}

type batchManifestDocument struct {
	SchemaVersion string        `json:"schema_version"`
	BatchID       string        `json:"batch_id"`
	Jobs          []manifestJob `json:"jobs"`
}

// --- Tyson preset corpus ------------------------------------------------------

// tysonEntity is one entity portrait row of the fixed request.
type tysonEntity struct {
	entityID    string
	name        string
	file        string
	preset      string
	mediaType   string
	attribution string
	source      string
}

var tysonEntities = []tysonEntity{
	{"person:mike-tyson", "Mike Tyson", "mike_tyson.png", "image_focus_in", "image/png",
		"AI editorial illustration, generated for this project", "Generated for this project"},
	{"person:cus-damato", "Cus D’Amato", "cus_damato.png", "image_fade_in", "image/png",
		"AI editorial illustration, generated for this project", "Generated for this project"},
	{"person:muhammad-ali", "Muhammad Ali", "muhammad_ali.jpg", "image_scale_in", "image/jpeg",
		"Wikimedia Commons, public-domain status stated on source page",
		"https://commons.wikimedia.org/wiki/File:Muhammad_Ali_Smiling_1962_Portrait.jpg"},
	{"person:sugar-ray-robinson", "Sugar Ray Robinson", "sugar_ray_robinson.jpg", "image_slide_left", "image/jpeg",
		"Library of Congress / Wikimedia Commons; no known copyright restriction stated on source page",
		"https://commons.wikimedia.org/wiki/File:Sugar_Ray_Robinson_1965_(cropped).jpg"},
	{"person:joe-frazier", "Joe Frazier", "joe_frazier.jpg", "image_slide_right", "image/jpeg",
		"Associated Press photo via Wikimedia Commons; U.S. public-domain/no-notice status stated on source page",
		"https://commons.wikimedia.org/wiki/File:Joe_Frazier_1971_Press_Photo.jpg"},
}

// tysonEntrance describes the reveal window the five image presets render with.
// It is recorded in the manifest so the report states the motion duration that
// was actually requested instead of implying one.
const (
	imageEntranceFrames  = 37 // 36 frame intervals = 1.5 s at 24 fps
	imageEntranceSeconds = 1.5
)

// TysonBuildOptions configures the Mike Tyson preset corpus build.
type TysonBuildOptions struct {
	// RequestPath is the fixed 2,000-word request the phrases come from.
	RequestPath string
	// BatchID scopes every derived job id.
	BatchID string
	// AssetBaseURL is the HTTP(S) root the worker self-heals assets from.
	AssetBaseURL string
	// RepoRoot is the RenderingGen checkout (font hashing + relative paths).
	RepoRoot string
	// OutPath is the manifest to write.
	OutPath string
	// PlanDir, when set, receives one semantic plan per job.
	PlanDir string
}

// BuildTysonManifest reads the fixed request and writes the batch manifest for
// the ten phrase overlays and the five entity image overlays.
//
// The phrases are the request's own extraction block, and each one is checked
// against its source segment: a phrase that no longer appears in the segment it
// claims to come from is a stale request, not a render.
func BuildTysonManifest(opts TysonBuildOptions) (*BuildResult, error) {
	if opts.RequestPath == "" {
		return nil, fmt.Errorf("overlaybatch: -request is required")
	}
	if opts.RepoRoot == "" {
		opts.RepoRoot = "."
	}
	if opts.AssetBaseURL == "" {
		return nil, fmt.Errorf("overlaybatch: -asset-base-url is required (the worker fetches the corpus from it)")
	}
	base := strings.TrimRight(opts.AssetBaseURL, "/")

	raw, err := os.ReadFile(opts.RequestPath)
	if err != nil {
		return nil, fmt.Errorf("overlaybatch: read request: %w", err)
	}
	var request struct {
		Items []struct {
			ScriptParams struct {
				Segments []struct {
					ID         string `json:"id"`
					SourceText string `json:"source_text"`
				} `json:"segments"`
			} `json:"script_params"`
			MediaPlan struct {
				Extraction struct {
					ImportantPhrases []string `json:"important_phrases"`
				} `json:"extraction"`
			} `json:"media_plan"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("overlaybatch: decode request: %w", err)
	}
	if len(request.Items) != 1 {
		return nil, fmt.Errorf("overlaybatch: request carries %d items, want exactly 1", len(request.Items))
	}
	item := request.Items[0]
	phrases := item.MediaPlan.Extraction.ImportantPhrases
	segments := item.ScriptParams.Segments
	// The pairing of phrase index to motion is catalog data (ChrononTemplate's
	// selection), not a literal list here: the ten overlays stay visually
	// distinguishable because the catalog pairs each phrase with a distinct
	// official motion, in order.
	phraseMotions := motion.PhraseMotions()
	if len(phraseMotions) == 0 {
		return nil, fmt.Errorf("overlaybatch: the embedded ChrononTemplate catalog declares no phrase-motion selection")
	}
	if len(phrases) != len(phraseMotions) || len(segments) != len(phraseMotions) {
		return nil, fmt.Errorf("overlaybatch: request must carry %d segments and %d phrases, got %d and %d",
			len(phraseMotions), len(phraseMotions), len(segments), len(phrases))
	}
	for index, phrase := range phrases {
		if !strings.Contains(segments[index].SourceText, phrase) {
			return nil, fmt.Errorf("overlaybatch: phrase %d (%q) is not in its source segment %q",
				index+1, phrase, segments[index].ID)
		}
	}

	fonts, err := fontAssets(opts.RepoRoot, func(local string) string { return base + "/" + local })
	if err != nil {
		return nil, err
	}

	// The entity corpus (files, attribution) is RenderingGen's, but the preset
	// each portrait renders through must be one the catalog declares: a typo here
	// would otherwise only surface as a rejected plan after the batch was paid
	// for.
	imagePresets := make(map[string]bool)
	for _, id := range motion.OverlayPresetIDs(string(overlay.PresetImage)) {
		imagePresets[id] = true
	}
	for _, entity := range tysonEntities {
		if !imagePresets[entity.preset] {
			return nil, fmt.Errorf("overlaybatch: entity %s names image preset %q, which is not in the ChrononTemplate catalog", entity.entityID, entity.preset)
		}
	}

	document := batchManifestDocument{SchemaVersion: SchemaBatchManifestV1, BatchID: opts.BatchID}
	for index, phrase := range phrases {
		motionID := phraseMotions[index]
		jobID := fmt.Sprintf("phrase-%02d", index+1)
		plan, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
			PlanID:     jobID,
			ProjectID:  "mike-tyson-overlay-presets-v1",
			Language:   "en",
			Width:      canvasWidth,
			Height:     canvasHeight,
			FPSNum:     canvasFPSNum,
			FPSDen:     canvasFPSDen,
			DurationMS: durationMS,
			Background: &renderbatch.Surface{Kind: "color", Color: backgroundRGBA},
			Items: []renderbatch.PlanItem{{
				ID:         "important-phrase",
				SceneID:    segments[index].ID,
				Kind:       "important_phrase",
				TemplateID: "IMPORTANT_PHRASE",
				PresetID:   overlay.CanonicalTextPresetID,
				MotionID:   motionID,
				Text:       phrase,
				StartMS:    0,
				EndMS:      durationMS,
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("overlaybatch: build plan %s: %w", jobID, err)
		}
		document.Jobs = append(document.Jobs, manifestJob{
			ID:         jobID,
			RenderPlan: plan,
			Assets:     fonts,
			Family:     "phrase",
			Text:       phrase,
			MotionID:   motionID,
			PresetID:   overlay.CanonicalTextPresetID,
		})
	}

	entranceFrames := imageEntranceFrames
	entranceSeconds := imageEntranceSeconds
	for index, entity := range tysonEntities {
		jobID := fmt.Sprintf("image-%02d", index+1)
		localPath := filepath.Join(opts.RepoRoot, "mike_tyson_overlay_test", "preset_overlays_v1", "assets", "entities", entity.file)
		digest, err := sha256File(localPath)
		if err != nil {
			return nil, err
		}
		assetURL := base + "/mike_tyson_overlay_test/preset_overlays_v1/assets/entities/" + url.PathEscape(entity.file)
		length := int64(durationMS)
		plan, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
			PlanID:     jobID,
			ProjectID:  "mike-tyson-overlay-presets-v1",
			Language:   "en",
			Width:      canvasWidth,
			Height:     canvasHeight,
			FPSNum:     canvasFPSNum,
			FPSDen:     canvasFPSDen,
			DurationMS: durationMS,
			Background: &renderbatch.Surface{Kind: "color", Color: backgroundRGBA},
			Items: []renderbatch.PlanItem{{
				ID:         "entity-image",
				EntityID:   entity.entityID,
				Kind:       "entity_image",
				TemplateID: "IMAGE_OVERLAY",
				PresetID:   entity.preset,
				Text:       entity.name,
				DurationMS: &length,
				AssetRefs: []renderbatch.PlanAssetRef{{
					AssetID:   entity.entityID,
					SHA256:    digest,
					URL:       assetURL,
					MediaType: entity.mediaType,
				}},
				StartMS: 0,
				EndMS:   durationMS,
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("overlaybatch: build plan %s: %w", jobID, err)
		}
		assets := append([]queue.AssetRef{}, fonts...)
		assets = append(assets, queue.AssetRef{
			Hash:        digest,
			LogicalPath: "assets/entities/" + entity.file,
			SourceURL:   assetURL,
		})
		document.Jobs = append(document.Jobs, manifestJob{
			ID:                      jobID,
			RenderPlan:              plan,
			Assets:                  assets,
			Family:                  "image",
			Text:                    entity.name,
			PresetID:                entity.preset,
			EntityID:                entity.entityID,
			EntityName:              entity.name,
			Asset:                   entity.file,
			AssetSource:             entity.source,
			AssetAttribution:        entity.attribution,
			EntranceDurationFrames:  &entranceFrames,
			EntranceDurationSeconds: &entranceSeconds,
		})
	}

	return writeBuild(document, opts.OutPath, opts.PlanDir, len(phrases), len(tysonEntities))
}

// --- multilingual overlay matrix ---------------------------------------------

// matrixLanguages is the language set the PipelineGen production matrix renders.
var matrixLanguages = []string{"it", "en", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}

const (
	matrixImageFile        = "testdata/golden/gerard_butler.jpg"
	matrixImageAssetID     = "gerard_butler"
	matrixImageMediaType   = "image/jpeg"
	matrixImageLogicalPath = "assets/semantic/gerard_butler.jpg"
)

// MultilingualBuildOptions configures the multilingual matrix build.
type MultilingualBuildOptions struct {
	// TranslationsPath is the translations.json of record.
	TranslationsPath string
	BatchID          string
	AssetBaseURL     string
	RepoRoot         string
	// Languages restricts the matrix; empty renders the full production set.
	Languages []string
	// Only restricts the matrix to the named overlays.
	Only    []string
	OutPath string
	PlanDir string
}

// BuildMultilingualManifest writes the 10 overlays × N languages batch manifest.
//
// Every phrase job declares BOTH fonts: the preset's primary and Chronon3d's
// coverage companion. The worker resolves a job's assets relative to its
// workspace, and the engine's fallback stack is built by scanning the primary
// font's own directory, so an undeclared font does not exist for the shaper —
// the translated text then rasterises to nothing while the job still reports
// "completed".
func BuildMultilingualManifest(opts MultilingualBuildOptions) (*BuildResult, error) {
	if opts.TranslationsPath == "" {
		return nil, fmt.Errorf("overlaybatch: -translations is required")
	}
	if opts.RepoRoot == "" {
		opts.RepoRoot = "."
	}
	if opts.AssetBaseURL == "" {
		return nil, fmt.Errorf("overlaybatch: -asset-base-url is required (the worker fetches the corpus from it)")
	}
	base := strings.TrimRight(opts.AssetBaseURL, "/")

	languages := opts.Languages
	if len(languages) == 0 {
		languages = matrixLanguages
	}
	known := make(map[string]bool, len(matrixLanguages))
	for _, language := range matrixLanguages {
		known[language] = true
	}
	for _, language := range languages {
		if !known[language] {
			return nil, fmt.Errorf("overlaybatch: unknown language %q", language)
		}
	}
	wanted := make(map[string]bool, len(opts.Only))
	for _, id := range opts.Only {
		wanted[id] = true
	}

	raw, err := os.ReadFile(opts.TranslationsPath)
	if err != nil {
		return nil, fmt.Errorf("overlaybatch: read translations: %w", err)
	}
	var translations map[string]struct {
		Translations map[string]string `json:"translations"`
	}
	if err := json.Unmarshal(raw, &translations); err != nil {
		return nil, fmt.Errorf("overlaybatch: decode translations: %w", err)
	}

	// The matrix corpus is served from testdata/golden, where the fonts sit under
	// their logical path; the image is addressed by its file name at that root.
	fonts, err := fontAssets(opts.RepoRoot, func(local string) string {
		return base + "/" + strings.TrimPrefix(local, "testdata/golden/")
	})
	if err != nil {
		return nil, err
	}

	// The overlay rows and the image preset matrix are catalog selections: the
	// overlay ids are RenderingGen's corpus, but which motion each one renders
	// through — and which image presets the matrix exercises — is the
	// ChrononTemplate catalog's answer, not a second list in Go.
	phraseOverlays := motion.PhraseOverlays()
	imageOverlays := motion.ImageOverlays()
	if len(phraseOverlays) == 0 || len(imageOverlays) == 0 {
		return nil, fmt.Errorf("overlaybatch: the embedded ChrononTemplate catalog is missing its phrase or image overlay selection")
	}

	document := batchManifestDocument{SchemaVersion: SchemaBatchManifestV1, BatchID: opts.BatchID}
	phrases, images := 0, 0
	for _, row := range phraseOverlays {
		if len(wanted) > 0 && !wanted[row.ID] {
			continue
		}
		translation, ok := translations[row.ID]
		if !ok {
			return nil, fmt.Errorf("overlaybatch: no translation row for %s", row.ID)
		}
		for _, language := range languages {
			text, ok := translation.Translations[language]
			if !ok {
				return nil, fmt.Errorf("overlaybatch: %s has no %s translation", row.ID, language)
			}
			jobID := row.ID + "__" + language
			plan, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
				PlanID:     jobID,
				Language:   language,
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
					PresetID:   overlay.CanonicalTextPresetID,
					MotionID:   row.Motion,
					Text:       text,
					StartMS:    0,
					EndMS:      durationMS,
				}},
			})
			if err != nil {
				return nil, fmt.Errorf("overlaybatch: build plan %s: %w", jobID, err)
			}
			document.Jobs = append(document.Jobs, manifestJob{
				ID:         jobID,
				RenderPlan: plan,
				Assets:     fonts,
				Family:     "phrase",
				Text:       text,
				MotionID:   row.Motion,
				PresetID:   overlay.CanonicalTextPresetID,
			})
			phrases++
		}
	}

	imageDigest, err := sha256File(filepath.Join(opts.RepoRoot, matrixImageFile))
	if err != nil {
		return nil, err
	}
	imageURL := base + "/" + filepath.Base(matrixImageFile)
	imageAsset := queue.AssetRef{Hash: imageDigest, LogicalPath: matrixImageLogicalPath, SourceURL: imageURL}
	for _, preset := range imageOverlays {
		if len(wanted) > 0 && !wanted[preset] {
			continue
		}
		for _, language := range languages {
			jobID := preset + "__" + language
			plan, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
				PlanID:     jobID,
				Language:   language,
				Width:      canvasWidth,
				Height:     canvasHeight,
				FPSNum:     canvasFPSNum,
				FPSDen:     canvasFPSDen,
				DurationMS: durationMS,
				Background: &renderbatch.Surface{Kind: "color", Color: backgroundRGBA},
				Items: []renderbatch.PlanItem{{
					ID:         "item_1",
					EntityID:   matrixImageAssetID,
					Kind:       "entity_image",
					TemplateID: "IMAGE_OVERLAY",
					PresetID:   preset,
					Text:       matrixImageAssetID,
					AssetRefs: []renderbatch.PlanAssetRef{{
						AssetID:   matrixImageAssetID,
						SHA256:    imageDigest,
						URL:       imageURL,
						MediaType: matrixImageMediaType,
					}},
					StartMS: 0,
					EndMS:   durationMS,
				}},
			})
			if err != nil {
				return nil, fmt.Errorf("overlaybatch: build plan %s: %w", jobID, err)
			}
			document.Jobs = append(document.Jobs, manifestJob{
				ID:         jobID,
				RenderPlan: plan,
				Assets:     []queue.AssetRef{imageAsset},
				Family:     "image",
				Text:       matrixImageAssetID,
				PresetID:   preset,
				EntityID:   matrixImageAssetID,
				EntityName: matrixImageAssetID,
				Asset:      filepath.Base(matrixImageFile),
			})
			images++
		}
	}

	return writeBuild(document, opts.OutPath, opts.PlanDir, phrases, images)
}

// --- shared build helpers ----------------------------------------------------

// fontAssets hashes and describes the two fonts every phrase job declares.
//
// sourcePath returns the font's path RELATIVE TO THE ASSET BASE URL of the
// corpus, so the self-heal URL points at bytes a server rooted at that base can
// actually answer: the two corpora serve from different roots (the preset corpus
// serves the checkout, the matrix serves testdata/golden) and both manifest of
// record shapes are preserved.
func fontAssets(repoRoot string, sourcePath func(local string) string) ([]queue.AssetRef, error) {
	specs := []struct{ file, logical string }{{fontPoppins, logicalFontP}, {fontInter, logicalFontI}}
	assets := make([]queue.AssetRef, 0, len(specs))
	for _, spec := range specs {
		digest, err := sha256File(filepath.Join(repoRoot, spec.file))
		if err != nil {
			return nil, err
		}
		assets = append(assets, queue.AssetRef{
			Hash:        digest,
			LogicalPath: spec.logical,
			SourceURL:   sourcePath(spec.file),
		})
	}
	return assets, nil
}

// writeBuild validates the built document, writes the manifest (and optionally
// the per-job plans) and reports the counts.
func writeBuild(document batchManifestDocument, outPath, planDir string, phrases, images int) (*BuildResult, error) {
	if outPath == "" {
		return nil, fmt.Errorf("overlaybatch: -out is required")
	}
	if len(document.Jobs) == 0 {
		return nil, fmt.Errorf("overlaybatch: the build produced no jobs")
	}
	if planDir != "" {
		for _, job := range document.Jobs {
			if err := writePlanFile(filepath.Join(planDir, job.ID+".json"), job.RenderPlan); err != nil {
				return nil, err
			}
		}
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("overlaybatch: encode manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(outPath, append(raw, '\n'), 0o644); err != nil {
		return nil, err
	}
	return &BuildResult{
		BatchID: document.BatchID,
		Jobs:    len(document.Jobs),
		Phrases: phrases,
		Images:  images,
		OutPath: outPath,
		PlanDir: planDir,
	}, nil
}

func writePlanFile(path string, plan []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var indent json.RawMessage
	if err := json.Unmarshal(plan, &indent); err != nil {
		return fmt.Errorf("overlaybatch: plan %s is not valid JSON: %w", path, err)
	}
	out, err := json.MarshalIndent(indent, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// sha256File is the content address every asset reference carries.
func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("overlaybatch: hash %s: %w", path, err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("overlaybatch: hash %s: %w", path, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
