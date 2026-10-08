package overlay

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/countryflags"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	motioncert "github.com/Marcuss-ops/RenderingGen/renderinggen/motion-certification"
	previewassets "github.com/Marcuss-ops/RenderingGen/renderinggen/motion-preview"
)

// RuntimeMotionCertification is the recorded certification outcome of one
// motion. It is always published, so "no recorded run" travels as the explicit
// status unverified instead of a missing key a consumer could read as certified.
// The status describes the report snapshot, not the current build: the selection
// model names the snapshot date and the reports it came from.
type RuntimeMotionCertification struct {
	Status      string `json:"status"`
	Report      string `json:"report,omitempty"`
	GeneratedAt string `json:"generated_at_utc,omitempty"`
	Error       string `json:"error,omitempty"`
}

// RuntimeMotionPreview is the recorded preview POINTER for one motion. The clip
// itself is gitignored, so the catalog publishes where the preview was produced
// and, only when the gallery named it mechanically, which clip belongs to the
// motion. Status recorded means the ledger has an entry, not that the media file
// is present on this machine.
type RuntimeMotionPreview struct {
	Status   string `json:"status"`
	Artifact string `json:"artifact,omitempty"`
	Media    string `json:"media,omitempty"`
}

// RuntimeMotionOption is the renderer-facing, read-only description of one
// registered animation choice. Targets are copied from the canonical motion
// catalog when declared; a missing target list is reported as undeclared and
// does not make the motion selectable for a target.
type RuntimeMotionOption struct {
	ID                 string `json:"id"`
	MapRenderer        string `json:"map_renderer,omitempty"`
	MapID              string `json:"map_id,omitempty"`
	MapAnimation       string `json:"map_animation,omitempty"`
	MapFlagCountryCode string `json:"map_flag_country_code,omitempty"`
	Deprecated         bool   `json:"deprecated,omitempty"`
	RemoveAfter        string `json:"remove_after,omitempty"`
	DeprecationNote    string `json:"deprecation_note,omitempty"`

	Family          string                     `json:"family,omitempty"`
	Targets         []string                   `json:"targets,omitempty"`
	TargetsDeclared bool                       `json:"targets_declared"`
	EnterFrames     int                        `json:"enter_frames,omitempty"`
	ExitFrames      int                        `json:"exit_frames,omitempty"`
	DurationBounds  *motion.DurationBounds     `json:"duration_bounds,omitempty"`
	Requires3D      bool                       `json:"requires_3d,omitempty"`
	RequiresCamera  bool                       `json:"requires_camera,omitempty"`
	Certification   RuntimeMotionCertification `json:"certification"`
	Preview         RuntimeMotionPreview       `json:"preview"`
}

// certificationSnapshotOnce parses the embedded reports once per process: the
// payload is compiled in, so re-reading it per motion would repeat identical
// work and could not change the result.
var (
	certificationSnapshotOnce  sync.Once
	certificationSnapshotValue motioncert.Snapshot
	certificationSnapshotErr   error
)

// certificationSnapshot returns the cached report snapshot. The error is
// returned rather than swallowed: the selection model publishes it, so a corrupt
// report cannot silently degrade into "all motions unverified".
func certificationSnapshot() (motioncert.Snapshot, error) {
	certificationSnapshotOnce.Do(func() {
		certificationSnapshotValue, certificationSnapshotErr = motioncert.Load()
	})
	return certificationSnapshotValue, certificationSnapshotErr
}

// runtimeMotionCertification publishes one motion's recorded status. When the
// snapshot itself is unavailable the motion stays unverified; the selection
// model carries the parse error for the consumer.
func runtimeMotionCertification(snapshot motioncert.Snapshot, id string) RuntimeMotionCertification {
	status := snapshot.Status(id)
	return RuntimeMotionCertification{
		Status:      status.Status,
		Report:      status.Report,
		GeneratedAt: status.GeneratedAt,
		Error:       status.Error,
	}
}

// previewRegistryOnce parses the embedded preview ledger once per process, for
// the same reason the certification snapshot is cached: the payload is compiled
// in and re-reading it per motion could not change the answer.
var (
	previewRegistryOnce  sync.Once
	previewRegistryValue previewassets.Registry
	previewRegistryErr   error
)

// previewRegistry returns the cached ledger. The error is published by the
// selection model rather than swallowed, so a corrupt ledger cannot degrade into
// a payload that says no motion has a preview.
func previewRegistry() (previewassets.Registry, error) {
	previewRegistryOnce.Do(func() {
		previewRegistryValue, previewRegistryErr = previewassets.Load()
	})
	return previewRegistryValue, previewRegistryErr
}

// runtimeMotionPreview publishes one motion's preview pointer. A motion the
// ledger never recorded stays "none"; the model carries any ledger error.
func runtimeMotionPreview(registry previewassets.Registry, id string) RuntimeMotionPreview {
	entry, ok := registry.EntryFor(id)
	if !ok {
		return RuntimeMotionPreview{Status: previewStatusNone}
	}
	return RuntimeMotionPreview{Status: previewStatusRecorded, Artifact: entry.Artifact, Media: entry.Media}
}

func runtimeMotionCatalog() ([]RuntimeMotionOption, map[string]*motion.MotionDefinition) {
	snapshot, _ := certificationSnapshot()
	registry, _ := previewRegistry()
	ids := motion.Registry.List()
	options := make([]RuntimeMotionOption, 0, len(ids))
	definitions := make(map[string]*motion.MotionDefinition, len(ids))
	for _, id := range ids {
		option := RuntimeMotionOption{
			ID:            id,
			Certification: runtimeMotionCertification(snapshot, id),
			Preview:       runtimeMotionPreview(registry, id),
		}
		if deprecation, ok := motion.Registry.DeprecationInfo(id); ok {
			option.Deprecated = true
			option.RemoveAfter = deprecation.RemoveAfter
			option.DeprecationNote = deprecation.Reason
		}
		definition, err := resolveMotionDefinition(id)
		if err == nil && definition != nil {
			definitions[id] = definition
			option.Family = definition.Category
			option.MapRenderer = definition.MapRenderer
			option.MapID = definition.MapID
			option.MapAnimation = definition.MapAnimation
			if definition.MapID != "" {
				if flag, ok := countryflags.LookupByName(definition.MapID); ok {
					option.MapFlagCountryCode = flag.CountryCode
				}
			}
			option.Targets = append([]string(nil), definition.Targets...)
			option.TargetsDeclared = definition.Targets != nil
			option.EnterFrames = definition.Enter
			option.ExitFrames = definition.Exit
			option.Requires3D = definition.Requires3D != nil && *definition.Requires3D
			option.RequiresCamera = definition.RequiresCamera != nil && *definition.RequiresCamera
			if definition.DurationBounds != nil {
				bounds := *definition.DurationBounds
				option.DurationBounds = &bounds
			}
		}
		options = append(options, option)
	}
	return options, definitions
}

func runtimeMotionFamilies(options []RuntimeMotionOption) map[string][]RuntimeMotionOption {
	families := make(map[string][]RuntimeMotionOption)
	for _, option := range options {
		if option.Deprecated || !option.TargetsDeclared || len(option.Targets) == 0 || option.Family == "" {
			continue
		}
		families[option.Family] = append(families[option.Family], option)
	}
	return families
}

func runtimeMotionFamilyIDs(families map[string][]RuntimeMotionOption) []string {
	ids := make([]string, 0, len(families))
	for id := range families {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// RuntimeAnimationUseCase describes a selectable composition. Scope is omitted
// for item-level cases and set to "plan" for plan-owned fields such as a
// background. MotionIDs lists only choices exposed for that usage; a nil slice
// means the composition has no dedicated family yet.
type RuntimeAnimationUseCase struct {
	ID                string                       `json:"id"`
	Scope             string                       `json:"scope,omitempty"`
	Zone              string                       `json:"zone,omitempty"`
	ItemKinds         []string                     `json:"item_kinds,omitempty"`
	Cardinality       string                       `json:"cardinality"`
	Description       string                       `json:"description"`
	Composition       *RuntimeAnimationComposition `json:"composition,omitempty"`
	BackgroundKind    string                       `json:"background_kind,omitempty"`
	MotionIDs         []string                     `json:"motion_ids"`
	StyleCountDefault int                          `json:"style_count_default,omitempty"`
	StyleCountMaximum int                          `json:"style_count_maximum,omitempty"`
}

// RuntimeAnimationComposition makes picker cardinality machine-readable.
// Captions is nil for image-only compositions and a bounded count for
// image-with-text compositions; Maps is used by map compositions.
type RuntimeAnimationComposition struct {
	ImageCount int                  `json:"image_count,omitempty"`
	Caption    *RuntimeCaptionCount `json:"caption_count,omitempty"`
	MapCount   int                  `json:"map_count,omitempty"`
}

type RuntimeCaptionCount struct {
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
}

func runtimeAnimationUseCases(allOptions []RuntimeMotionOption, definitions map[string]*motion.MotionDefinition) []RuntimeAnimationUseCase {
	groups := collectRuntimeAnimationMotionGroups(allOptions, definitions)
	imageKinds := []string{"entity_image", "image", "image_popup", "product", "logo"}
	captionKinds := []string{
		string(KindEntityCard), string(KindOrganization), string(KindLocation), string(KindConcept),
		string(KindEntityImage), "image", string(KindImagePopup), string(KindProduct), string(KindLogo), string(KindLightLeak),
	}
	useCases := []RuntimeAnimationUseCase{
		{ID: "important_phrase", ItemKinds: []string{"important_phrase"}, Cardinality: "one text layer", Description: "Editorial phrase motion; choose a text preset separately.", MotionIDs: groups.phrases},
		{ID: "short_important_phrase", ItemKinds: []string{"important_phrase"}, Cardinality: "one text layer", Description: "Short-phrase styles and modern typewriter motions; choose a text preset separately.", MotionIDs: groups.shortPhrases},
	}
	for _, definition := range imageCompositionCatalog {
		cardinality := imageCompositionCardinality(definition.Composition)
		useCase := RuntimeAnimationUseCase{
			ID: definition.ID, ItemKinds: imageKinds, Cardinality: cardinality,
			Description: compositionDescription(cardinality, definition.Description), Composition: cloneRuntimeAnimationComposition(definition.Composition),
		}
		useCase.MotionIDs = groups.forTarget(definition.MotionTarget)
		useCases = append(useCases, useCase)
	}
	for _, definition := range mapCompositionCatalog {
		cardinality := mapCompositionCardinality(definition.MapCount)
		useCase := RuntimeAnimationUseCase{
			ID: definition.ID, ItemKinds: []string{"map"}, Cardinality: cardinality,
			Description: definition.Description,
			Composition: &RuntimeAnimationComposition{MapCount: definition.MapCount},
		}
		useCase.MotionIDs = groups.forTarget(definition.MotionTarget)
		useCases = append(useCases, useCase)
	}
	for _, definition := range backgroundCompositionCatalog {
		if !definition.Supported {
			// Declared-but-unsupported sources stay out of the picker's use
			// cases: the runtime selection surface lists them via the selection
			// model's background sources, and the published schema enum for
			// background_kind covers only the lowering-supported kinds.
			continue
		}
		useCases = append(useCases, RuntimeAnimationUseCase{
			ID: definition.ID, Scope: "plan", Cardinality: "one canvas background",
			Description: definition.Description, BackgroundKind: definition.Kind, MotionIDs: nil,
		})
	}
	useCases = append(useCases,
		RuntimeAnimationUseCase{ID: "entities", ItemKinds: []string{string(KindEntityCard), string(KindOrganization), string(KindLocation), string(KindConcept), string(KindEntityImage)}, Cardinality: "one image layer per entity", Description: "Entity images rotate through their own bounded image-motion pool; caption styles remain independently selectable.", MotionIDs: groups.forTarget("image")},
		RuntimeAnimationUseCase{ID: "entity_caption", ItemKinds: captionKinds, Cardinality: "one caption per image or composite child", Description: "Caption motion is selected separately from the image motion, independent of whether the image represents an entity.", MotionIDs: groups.captions},
	)
	for _, definition := range dataCompositionCatalog {
		useCases = append(useCases, RuntimeAnimationUseCase{
			ID: definition.ID, ItemKinds: []string{string(definition.Kind)},
			Cardinality: definition.Cardinality, Description: definition.Description,
			MotionIDs: groups.forTarget(definition.MotionTarget),
		})
	}
	// The product zone is derived from the composition owners, never declared
	// twice: a use case added without a zone would show up as unzoned instead of
	// silently landing in someone else's picker area.
	for index := range useCases {
		useCases[index].Zone = runtimeUseCaseZone(useCases[index].ID)
		if len(useCases[index].MotionIDs) > 0 {
			useCases[index].StyleCountDefault = min(5, len(useCases[index].MotionIDs))
			useCases[index].StyleCountMaximum = len(useCases[index].MotionIDs)
		}
	}
	return useCases
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type runtimeAnimationMotionGroups struct {
	shortPhrases []string
	phrases      []string
	images       []string
	maps         []string
	captions     []string
	metrics      []string
	dates        []string
}

func (groups runtimeAnimationMotionGroups) forTarget(target string) []string {
	switch target {
	case "image":
		return groups.images
	case "map_view":
		return groups.maps
	case "metric":
		return groups.metrics
	case "date":
		return groups.dates
	default:
		return nil
	}
}

func collectRuntimeAnimationMotionGroups(options []RuntimeMotionOption, definitions map[string]*motion.MotionDefinition) runtimeAnimationMotionGroups {
	var groups runtimeAnimationMotionGroups
	for _, option := range options {
		definition := definitions[option.ID]
		if definition == nil || option.Deprecated {
			continue
		}
		shortPhrase := motionDefinitionAdmitsTarget(definition, "short_phrase")
		if shortPhrase {
			groups.shortPhrases = append(groups.shortPhrases, option.ID)
		}
		// The two picker categories share an item kind but must stay disjoint.
		if !shortPhrase && motionDefinitionAdmitsTarget(definition, "important_phrase") {
			groups.phrases = append(groups.phrases, option.ID)
		}
		if motionDefinitionAdmitsTarget(definition, "image") && !isMultiImageRecipe(definition) {
			groups.images = append(groups.images, option.ID)
		}
		if motionDefinitionAdmitsTarget(definition, "map_view") {
			groups.maps = append(groups.maps, option.ID)
		}
		if motionDefinitionAdmitsTarget(definition, "caption") {
			groups.captions = append(groups.captions, option.ID)
		}
		if motionDefinitionAdmitsTarget(definition, "metric") {
			groups.metrics = append(groups.metrics, option.ID)
		}
		if motionDefinitionAdmitsTarget(definition, "date") {
			groups.dates = append(groups.dates, option.ID)
		}
	}
	return groups
}

// RuntimeAnimationCatalog is the versioned JSON payload served to picker/UI
// consumers. It is a compiled view of the canonical motion registry plus the
// semantic use cases supported by this compiler; it does not replace the
// source catalog.
type RuntimeAnimationCatalog struct {
	SchemaVersion int                       `json:"schema_version"`
	Families      []string                  `json:"families"`
	Motions       []RuntimeMotionOption     `json:"motions"`
	UseCases      []RuntimeAnimationUseCase `json:"use_cases"`
	// SelectionModel is the compiled picker model: zones, the compatibility
	// matrix, the layout constraints, the map layouts, the background sources,
	// the camera destinations and the declared metric/date fields.
	SelectionModel RuntimeSelectionModel `json:"selection_model"`
}

// CompiledRuntimeAnimationCatalog returns a deterministic snapshot suitable
// for JSON serialization and external UI consumption.
func CompiledRuntimeAnimationCatalog() RuntimeAnimationCatalog {
	options, definitions := runtimeMotionCatalog()
	families := runtimeMotionFamilies(options)
	useCases := runtimeAnimationUseCases(options, definitions)
	return RuntimeAnimationCatalog{
		SchemaVersion:  1,
		Families:       runtimeMotionFamilyIDs(families),
		Motions:        options,
		UseCases:       useCases,
		SelectionModel: runtimeSelectionModel(useCases, definitions),
	}
}

// WriteRuntimeAnimationCatalog writes the versioned, deterministic picker
// payload as JSON. Commands can redirect it to a file or HTTP response.
func WriteRuntimeAnimationCatalog(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(CompiledRuntimeAnimationCatalog()); err != nil {
		return fmt.Errorf("overlay: encode runtime animation catalog: %w", err)
	}
	return nil
}
