package overlaybatch

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	fontDejaVu   = "testdata/golden/assets/fonts/DejaVuSans.ttf"
	logicalFontP = "assets/fonts/Poppins-Bold.ttf"
	logicalFontI = "assets/fonts/Inter-Bold.ttf"
	logicalFontD = "assets/fonts/DejaVuSans.ttf"
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
	MotionPool              []string `json:"motion_pool,omitempty"`
	SelectionSeed           uint64   `json:"selection_seed,omitempty"`
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
// the phrase overlays (one per phrase motion the embedded ChrononTemplate
// catalog declares) and the five entity image overlays.
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
	// Motion choices come from the emitter-owned pool. A stable seed derived
	// from the plan and item ids makes repeated builds choose the same motion.
	phrasePool := motion.PhraseMotionPool()
	if len(phrasePool) == 0 {
		return nil, fmt.Errorf("overlaybatch: the embedded ChrononTemplate catalog declares no phrase_motion_pool")
	}
	if len(phrases) == 0 || len(segments) != len(phrases) {
		return nil, fmt.Errorf("overlaybatch: request must carry the same non-zero number of segments and phrases, got %d and %d",
			len(segments), len(phrases))
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
		jobID := fmt.Sprintf("phrase-%02d", index+1)
		selectionSeed := phraseSelectionSeed(jobID, "important-phrase")
		motionID, err := selectPhraseMotion(phrasePool, selectionSeed)
		if err != nil {
			return nil, fmt.Errorf("overlaybatch: select motion for %s: %w", jobID, err)
		}
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
				PresetID:   overlay.PhraseDefaultPresetID,
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
			ID:            jobID,
			RenderPlan:    plan,
			Assets:        fonts,
			Family:        "phrase",
			Text:          phrase,
			MotionID:      motionID,
			MotionPool:    append([]string(nil), phrasePool...),
			SelectionSeed: selectionSeed,
			PresetID:      overlay.PhraseDefaultPresetID,
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

// ImageMotionBuildOptions builds a GPU-renderable image-motion matrix using
// one fixed, content-addressed image so pairwise frame differences isolate the
// motion itself.
type ImageMotionBuildOptions struct {
	BatchID      string
	AssetBaseURL string
	RepoRoot     string
	OutPath      string
	PlanDir      string
}

func BuildImageMotionManifest(opts ImageMotionBuildOptions) (*BuildResult, error) {
	if opts.BatchID == "" || opts.AssetBaseURL == "" || opts.OutPath == "" {
		return nil, fmt.Errorf("overlaybatch: image-motion build requires batch id, asset base URL, and output path")
	}
	if opts.RepoRoot == "" {
		opts.RepoRoot = "."
	}
	base := strings.TrimRight(opts.AssetBaseURL, "/")
	localPath := filepath.Join(opts.RepoRoot, matrixImageFile)
	digest, err := sha256File(localPath)
	if err != nil {
		return nil, err
	}
	assetURL := base + "/" + matrixImageFile
	length := int64(durationMS)
	pool := motion.Registry.ImageOverlayMotionIDs()
	if len(pool) != 18 {
		return nil, fmt.Errorf("overlaybatch: image motion inventory has %d entries, want 18", len(pool))
	}
	document := batchManifestDocument{SchemaVersion: SchemaBatchManifestV1, BatchID: opts.BatchID}
	for index, motionID := range pool {
		jobID := fmt.Sprintf("image-motion-%02d", index+1)
		plan, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
			PlanID: jobID, ProjectID: "image-motion-certification-v1", Language: "en",
			Width: canvasWidth, Height: canvasHeight, FPSNum: canvasFPSNum, FPSDen: canvasFPSDen,
			DurationMS: durationMS,
			Background: &renderbatch.Surface{Kind: "color", Color: backgroundRGBA},
			Items: []renderbatch.PlanItem{{
				ID: "image-motion", EntityID: "entity:image-motion-canary", Kind: "entity_image",
				TemplateID: "IMAGE_OVERLAY", PresetID: "image_focus_in", MotionID: motionID,
				Text: "Image motion canary", DurationMS: &length,
				AssetRefs: []renderbatch.PlanAssetRef{{AssetID: matrixImageAssetID, SHA256: digest, URL: assetURL, MediaType: matrixImageMediaType}},
				StartMS:   0, EndMS: durationMS,
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("overlaybatch: build image motion plan %s (%s): %w", jobID, motionID, err)
		}
		document.Jobs = append(document.Jobs, manifestJob{
			ID: jobID, RenderPlan: plan,
			Assets: []queue.AssetRef{{Hash: digest, LogicalPath: matrixImageLogicalPath, SourceURL: assetURL}},
			Family: "image", MotionID: motionID, PresetID: "image_focus_in", Asset: filepath.Base(matrixImageFile),
			AssetSource: "RenderingGen testdata golden image; fixed across motion matrix",
		})
	}
	return writeBuild(document, opts.OutPath, opts.PlanDir, 0, len(pool))
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

func phraseSelectionSeed(planID, itemID string) uint64 {
	digest := sha256.Sum256([]byte(planID + "\x00" + itemID))
	return binary.BigEndian.Uint64(digest[:8])
}

func selectPhraseMotion(pool []string, seed uint64) (string, error) {
	if len(pool) == 0 {
		return "", fmt.Errorf("motion pool is empty")
	}
	seen := make(map[string]struct{}, len(pool))
	for _, id := range pool {
		if strings.TrimSpace(id) == "" {
			return "", fmt.Errorf("motion pool contains an empty id")
		}
		if _, duplicate := seen[id]; duplicate {
			return "", fmt.Errorf("motion pool repeats %q", id)
		}
		seen[id] = struct{}{}
		if _, err := motion.Registry.Resolve(id); err != nil {
			return "", fmt.Errorf("motion pool contains unknown id %q: %w", id, err)
		}
	}
	var input [8]byte
	binary.BigEndian.PutUint64(input[:], seed)
	digest := sha256.Sum256(input[:])
	index := binary.BigEndian.Uint64(digest[:8]) % uint64(len(pool))
	return pool[index], nil
}

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
// Every phrase job declares all closed-enum bundled font families accepted by
// per-item runtime overrides, plus any language-selected primary not already
// included. See fontAssetsForLanguage for why that set is derived from the
// worker's font vocabulary instead of restated here. The worker resolves a
// job's assets relative to its workspace and the engine's fallback stack is
// built by scanning the primary font's own directory, so an undeclared font does not exist for the shaper —
// the translated text then either rasterises to nothing while the job still
// reports "completed", or the render dies in preflight with exit 1.
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
	sourcePath := func(local string) string {
		return base + "/" + strings.TrimPrefix(local, "testdata/golden/")
	}
	fontsByLanguage := make(map[string][]queue.AssetRef, len(languages))
	for _, language := range languages {
		fonts, err := fontAssetsForLanguage(opts.RepoRoot, sourcePath, language)
		if err != nil {
			return nil, err
		}
		fontsByLanguage[language] = fonts
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
					PresetID:   overlay.PhraseDefaultPresetID,
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
				Assets:     fontsByLanguage[language],
				Family:     "phrase",
				Text:       text,
				MotionID:   row.Motion,
				PresetID:   overlay.PhraseDefaultPresetID,
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

// fontAssets hashes and describes every bundled font family accepted by item overrides.
//
// sourcePath returns the font's path RELATIVE TO THE ASSET BASE URL of the
// corpus, so the self-heal URL points at bytes a server rooted at that base can
// actually answer: the two corpora serve from different roots (the preset corpus
// serves the checkout, the matrix serves testdata/golden) and both manifest of
// record shapes are preserved.
func fontAssets(repoRoot string, sourcePath func(local string) string) ([]queue.AssetRef, error) {
	return fontAssetsForLanguage(repoRoot, sourcePath, "")
}

// fontSpec is one font a job declares: the checkout file to hash and the
// logical path the plan refers to it by.
type fontSpec struct{ file, logical string }

// fontLocalFiles maps a plan's font logical path to the checkout file that
// backs it. A logical path with no entry here is one this builder cannot serve,
// and that is a hard error rather than a silently undeclared font.
var fontLocalFiles = map[string]string{
	logicalFontP: fontPoppins,
	logicalFontI: fontInter,
	logicalFontD: fontDejaVu,
}

// fontAssetsForLanguage declares all fonts a language's jobs can select at
// runtime (Poppins, Inter and DejaVu Sans), ensuring a valid enum override is
// always materializable, plus the language-selected primary if it is new.
//
// godlike/06 SSOT: the extra font is derived from
// overlay.OfficialFontPathForLanguage — the same owner the plan compiler uses —
// so the manifest can never declare a different font than the plan burns. That
// drift is exactly the Cyrillic failure: the worker resolves a job's assets
// against its workspace and Chronon builds its fallback stack by scanning the
// PRIMARY font's directory, so a plan font the manifest omits does not exist for
// the shaper and the render dies in preflight (exit 1).
func fontAssetsForLanguage(repoRoot string, sourcePath func(local string) string, language string) ([]queue.AssetRef, error) {
	return fontAssetsFromSpecs(repoRoot, sourcePath, fontSpecsForLanguage(language))
}

// fontSpecsForLanguage returns all runtime-selectable font assets and ensures
// the plan's language-selected primary font is included.
func fontSpecsForLanguage(language string) []fontSpec {
	specs := []fontSpec{{fontPoppins, logicalFontP}, {fontInter, logicalFontI}, {fontDejaVu, logicalFontD}}
	primary := overlay.OfficialFontPathForLanguage(language)
	if local, ok := fontLocalFiles[primary]; ok && !hasLogicalFont(specs, primary) {
		specs = append(specs, fontSpec{local, primary})
	}
	return specs
}

func hasLogicalFont(specs []fontSpec, logical string) bool {
	for _, spec := range specs {
		if spec.logical == logical {
			return true
		}
	}
	return false
}

func fontAssetsFromSpecs(repoRoot string, sourcePath func(local string) string, specs []fontSpec) ([]queue.AssetRef, error) {
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
	// Fail closed BEFORE anything is written: a manifest whose plan asks a shaper
	// for a font the job does not declare is a render that dies in preflight,
	// and it must not reach the queue at all.
	if err := checkPlanFonts(document); err != nil {
		return nil, err
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

// checkPlanFonts refuses a manifest whose plan asks the shaper for a font the
// job does not declare.
//
// The plan is lowered the same way the worker lowers it (renderbatch's single
// compiler), so this compares what will actually be rendered against what will
// actually be materialised. The failure it prevents is the Cyrillic rehearsal
// one: the plan burned assets/fonts/DejaVuSans.ttf, the job declared only the
// Latin pair, and the daemon exited 1 at frame 0 with "no font in stack covers
// all visible codepoints" — a whole language lost to a manifest that was
// internally inconsistent while still being schema-valid.
func checkPlanFonts(document batchManifestDocument) error {
	for _, job := range document.Jobs {
		missing, err := undeclaredPlanFonts(job.RenderPlan, job.Assets)
		if err != nil {
			return fmt.Errorf("overlaybatch: job %s: %w", job.ID, err)
		}
		if len(missing) > 0 {
			return fmt.Errorf("overlaybatch: job %s renders with %s but declares no such font (declared: %s): the shaper would have no glyphs for it, so the plan and the manifest must be made to agree",
				job.ID, strings.Join(missing, ", "), strings.Join(logicalPaths(job.Assets), ", "))
		}
	}
	return nil
}

// undeclaredPlanFonts is the fonts a job's CONCRETE plan references minus the
// ones the job declares. A job with no text layers (an image overlay) has none.
func undeclaredPlanFonts(semanticPlan []byte, declared []queue.AssetRef) ([]string, error) {
	concrete, err := renderbatch.CompileRenderPlan(semanticPlan)
	if err != nil {
		return nil, fmt.Errorf("compile plan: %w", err)
	}
	var document struct {
		Layers []struct {
			Style *struct {
				Font string `json:"font"`
			} `json:"style"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(concrete, &document); err != nil {
		return nil, fmt.Errorf("decode compiled plan: %w", err)
	}
	have := make(map[string]bool, len(declared))
	for _, asset := range declared {
		have[asset.LogicalPath] = true
	}
	seen := make(map[string]bool, len(document.Layers))
	var missing []string
	for _, layer := range document.Layers {
		if layer.Style == nil || layer.Style.Font == "" || have[layer.Style.Font] || seen[layer.Style.Font] {
			continue
		}
		seen[layer.Style.Font] = true
		missing = append(missing, layer.Style.Font)
	}
	sort.Strings(missing)
	return missing, nil
}

// logicalPaths is the declared logical paths of an asset list.
func logicalPaths(assets []queue.AssetRef) []string {
	paths := make([]string, 0, len(assets))
	for _, asset := range assets {
		paths = append(paths, asset.LogicalPath)
	}
	return paths
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
