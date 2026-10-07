// selection_model.go owns the machine-readable selection model a picker/UI
// consumer reads from the compiled runtime catalog: the product zones, the
// composition × motion target × renderer capability matrix, the per-composition
// layout constraints, the map layout capabilities, the supported background
// sources, the camera destinations and the declared metric/date fields.
//
// Why it is a compiled VIEW and not a second catalog. Every fact here already
// has an owner in this package: the composition catalogs own cardinality, the
// canonical motion targets own applicability, the design tokens own the derived
// geometry, and the compiler owns what each lowering actually accepts. The
// model derives from those owners so a UI cannot render a combination the
// compiler would reject, and no motion ID is restated here.
package overlay

import (
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// Product zones. They are navigable areas of the library, deliberately
// distinct from an item kind, a template, a preset and a motion family.
const (
	ZoneImages         = "images"
	ZoneImagesWithText = "images_with_text"
	ZoneMaps           = "maps"
	ZoneBackground     = "background"
	ZoneData           = "data"
	ZoneCameraRoll     = "camera_roll"
)

// Zone status values. A zone with no authored composition is declared, not
// hidden: the picker shows it as empty instead of inventing a family.
const (
	zoneStatusAvailable     = "available"
	zoneStatusNoComposition = "no_composition_authored"
)

// Published preview-pointer status. recorded means the ledger has an entry for
// the motion, not that the media is present: the clips are gitignored by policy
// and are regenerated from the artifact the ledger names.
const (
	previewStatusRecorded = "recorded"
	previewStatusNone     = "none"
)

// Renderer capability vocabulary. It is the third axis of the compatibility
// matrix: a composition whose motions need 3D, a camera controller or the
// geospatial plane cannot run on a 2D-only renderer.
const (
	capability2D         = "2d"
	capability3D         = "3d"
	capabilityCamera     = "camera"
	capabilityGeospatial = "geospatial"
)

// Declared layout constraints. They are catalog vocabulary, not lowering
// tokens: the lowering already honours them, and publishing them here lets a UI
// preview the same rule the compiler enforces.
const (
	// compositionAspectRatioRule: every cell preserves the aspect ratio of its
	// own asset instead of being stretched to a shared cell ratio.
	compositionAspectRatioRule = "preserve_source_per_cell"
	// compositionCropMode: a cell that is not the asset's ratio crops with
	// cover, never letterboxes.
	compositionCropMode = "cover"
	// compositionGapFraction is the canvas fraction kept between adjacent
	// cells of a counted composition.
	compositionGapFraction = 0.02
	// compositionLayerOrder: image_layers composite in declaration order.
	compositionLayerOrder = "declaration_order"
	// compositionActiveLayerSingle / Multi: a single image has nothing to
	// select; a counted composition selects the emphasized child by
	// motion_params.active_layer_id.
	compositionActiveLayerSingle = "implicit"
	compositionActiveLayerMulti  = "producer_selected_active_layer_id"
	// compositionMissingAsset: a layer whose asset_ref is not declared fails
	// the whole compile instead of being dropped or substituted.
	compositionMissingAsset = "fail_closed"
	// compositionCaptionEntryOrder and captionTextLimitLines() are the
	// dense-layout rules: captions enter in declaration order and a caption box
	// wraps to at most the derived number of lines before the packer reports a
	// collision.
	compositionCaptionEntryOrder = "declaration_order"
	// compositionCaptionUseCase names the picker case that owns caption
	// motions, so an image-with-text composition points at the caption selector
	// instead of publishing a second, duplicated caption motion list.
	compositionCaptionUseCase = "entity_caption"
	// Motion scope answers "does the motion act on the composition or on its
	// children" for every counted composition, so a UI never offers a
	// composition-level motion the compiler cannot lower. A single image has
	// one motion for its one item; two or more images animate per layer. When a
	// caption exists its motion is selected independently per caption.
	compositionMotionScopeItem       = "item"
	compositionMotionScopePerLayer   = "per_layer"
	compositionMotionScopePerCaption = "per_caption"
)

// RuntimeZone is one navigable area of the library.
type RuntimeZone struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Description    string   `json:"description"`
	Status         string   `json:"status"`
	CompositionIDs []string `json:"composition_ids"`
}

// RuntimeCompatibilityEntry is one cell of the generated matrix
// composition × motion target × renderer capability. Available is false when
// no dedicated family has been authored, and the required capabilities name
// what a renderer would have to support before the cell can become selectable.
type RuntimeCompatibilityEntry struct {
	CompositionID        string   `json:"composition_id"`
	Zone                 string   `json:"zone,omitempty"`
	MotionTarget         string   `json:"motion_target"`
	MotionFamilies       []string `json:"motion_families"`
	MotionCount          int      `json:"motion_count"`
	Available            bool     `json:"available"`
	RequiredCapabilities []string `json:"required_capabilities"`
	UnavailableReason    string   `json:"unavailable_reason,omitempty"`
}

// RuntimeCompositionLayout is the declared layout contract of one image
// composition: the constraints a UI must preview and the compiler enforces.
type RuntimeCompositionLayout struct {
	ID                 string  `json:"id"`
	CompositionID      string  `json:"composition_id"`
	Zone               string  `json:"zone"`
	AspectRatio        string  `json:"aspect_ratio"`
	CropMode           string  `json:"crop_mode"`
	SafeAreaFraction   float64 `json:"safe_area_fraction"`
	GapFraction        float64 `json:"gap_fraction"`
	LayerOrder         string  `json:"layer_order"`
	ActiveLayer        string  `json:"active_layer"`
	MissingAsset       string  `json:"missing_asset_behavior"`
	MotionScope        string  `json:"motion_scope"`
	MaxCaptions        int     `json:"max_captions,omitempty"`
	CaptionMotionScope string  `json:"caption_motion_scope,omitempty"`
	CaptionEntryOrder  string  `json:"caption_entry_order,omitempty"`
	CaptionUseCase     string  `json:"caption_use_case,omitempty"`
	TextLimitLines     int     `json:"text_limit_lines,omitempty"`
}

// RuntimeMapLayout is one map layout of the Maps zone and the motion targets
// projected onto it. Unsupported layouts are declared fail-closed so a producer
// cannot select a route or callout that has no lowering.
type RuntimeMapLayout struct {
	ID                     string   `json:"id"`
	CompositionID          string   `json:"composition_id"`
	Description            string   `json:"description"`
	Supported              bool     `json:"supported"`
	ProjectedMotionTargets []string `json:"projected_motion_targets"`
	UnavailableReason      string   `json:"unavailable_reason,omitempty"`
}

// RuntimeBackgroundSource is one background source family. Priority is the
// deterministic order the compiler resolves when a plan declares more than one.
type RuntimeBackgroundSource struct {
	Kind              string `json:"kind"`
	CompositionID     string `json:"composition_id,omitempty"`
	Description       string `json:"description"`
	Supported         bool   `json:"supported"`
	Coverage          string `json:"coverage"`
	RequiresAssets    bool   `json:"requires_assets"`
	FitPolicy         string `json:"fit_policy,omitempty"`
	Loop              bool   `json:"loop,omitempty"`
	Priority          int    `json:"priority"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// RuntimeDataField is one declared semantic field of a metric/date
// composition. Enforced is true because the wire contract carries the typed
// block, the Go mirror and the JSON Schema are pinned together, and the
// compiler refuses a declared block that is incomplete or that belongs to
// another target. Required means required inside a DECLARED block: the block
// itself stays optional so a plan that transports only the displayed text keeps
// compiling.
type RuntimeDataField struct {
	CompositionID string `json:"composition_id"`
	Name          string `json:"name"`
	Required      bool   `json:"required"`
	Enforced      bool   `json:"enforced"`
	Description   string `json:"description"`
}

// RuntimeCertificationReport mirrors one parsed certification report. Scope is
// derived from the artifact name, so a consumer can tell the image vocabulary
// from the phrase vocabulary without opening the file.
type RuntimeCertificationReport struct {
	Report      string `json:"report"`
	Scope       string `json:"scope"`
	Backend     string `json:"backend"`
	GeneratedAt string `json:"generated_at_utc"`
	Motions     int    `json:"motions"`
}

// RuntimeCertificationModel makes the recorded certification state
// machine-readable. It is deliberately explicit about being a snapshot:
// IsCurrent is false because the reports are dated artifacts, not a live
// certification of the build that produced this payload. Failed counts the
// recorded runs that did not pass, and Unverified counts the motions no report
// names — the number a picker must not present as certified.
type RuntimeCertificationModel struct {
	Source     string                       `json:"source"`
	SnapshotAt string                       `json:"snapshot_at,omitempty"`
	IsCurrent  bool                         `json:"is_current"`
	Rule       string                       `json:"rule"`
	Passed     int                          `json:"passed"`
	Failed     int                          `json:"failed"`
	Unverified int                          `json:"unverified"`
	Reports    []RuntimeCertificationReport `json:"reports"`
	Error      string                       `json:"error,omitempty"`
}

// RuntimePreviewModel makes the preview-asset ledger machine-readable. It is a
// pointer index, not a media list: every recorded preview names the artifact that
// declares it, and media stays out of git, so a consumer must regenerate the clip
// from the artifact before showing it. MediaInGit publishes that policy instead
// of leaving a consumer to discover it.
type RuntimePreviewModel struct {
	Source      string   `json:"source"`
	GeneratedAt string   `json:"generated_at_utc,omitempty"`
	Roots       []string `json:"roots"`
	MediaInGit  bool     `json:"media_in_git"`
	Recorded    int      `json:"recorded"`
	None        int      `json:"none"`
	Entries     int      `json:"entries"`
	Rule        string   `json:"rule"`
	Error       string   `json:"error,omitempty"`
}

// Deprecation policy vocabulary. The owner decision of 7 October 2026 is
// immediate retirement: a retired ID is non-selectable and not resolvable at
// once, with no compatibility window and no alias.
const deprecationPolicyImmediate = "immediate"

// RuntimeDeprecationEntry is one retired motion. The registry record is the
// source; this is the picker-facing projection of it.
type RuntimeDeprecationEntry struct {
	MotionID    string `json:"motion_id"`
	RetiredOn   string `json:"retired_on,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

// RuntimeDeprecationModel publishes the retirement policy and every retired
// motion. deprecated is always an array, so a consumer can tell "nothing is
// retired" from "the field was never published".
type RuntimeDeprecationModel struct {
	Policy         string                    `json:"policy"`
	WindowDays     int                       `json:"compatibility_window_days"`
	AliasesAllowed bool                      `json:"aliases_allowed"`
	Rule           string                    `json:"rule"`
	Deprecated     []RuntimeDeprecationEntry `json:"deprecated"`
}

// RuntimeCameraPolicy publishes the controller rule the compiler enforces.
type RuntimeCameraPolicy struct {
	MaxSceneControllers int    `json:"max_scene_controllers"`
	Rule                string `json:"rule"`
}

// RuntimeOwnership names the single canonical owner of every data type the
// picker consumes, so a consumer knows where a change belongs instead of
// editing the compiled payload.
type RuntimeOwnership struct {
	MotionDefinitions string `json:"motion_definitions"`
	SemanticContract  string `json:"semantic_contract"`
	CompositionModel  string `json:"composition_model"`
	RuntimeCatalog    string `json:"runtime_catalog"`
}

// RuntimeSelectionModel is the compiled selection model served with the runtime
// animation catalog.
type RuntimeSelectionModel struct {
	Zones              []RuntimeZone               `json:"zones"`
	Compatibility      []RuntimeCompatibilityEntry `json:"compatibility"`
	Layouts            []RuntimeCompositionLayout  `json:"layouts"`
	MapLayouts         []RuntimeMapLayout          `json:"map_layouts"`
	BackgroundSources  []RuntimeBackgroundSource   `json:"background_sources"`
	CameraDestinations []RuntimeCameraDestination  `json:"camera_destinations"`
	CameraPolicy       RuntimeCameraPolicy         `json:"camera_policy"`
	DataFields         []RuntimeDataField          `json:"data_fields"`
	Deprecation        RuntimeDeprecationModel     `json:"deprecation"`
	Certification      RuntimeCertificationModel   `json:"certification"`
	Preview            RuntimePreviewModel         `json:"preview"`
	Ownership          RuntimeOwnership            `json:"ownership"`
}

func runtimeSelectionModel(useCases []RuntimeAnimationUseCase, definitions map[string]*motion.MotionDefinition) RuntimeSelectionModel {
	return RuntimeSelectionModel{
		Zones:              runtimeZones(),
		Compatibility:      runtimeCompatibilityMatrix(useCases, definitions),
		Layouts:            runtimeCompositionLayouts(),
		MapLayouts:         runtimeMapLayouts(),
		BackgroundSources:  runtimeBackgroundSources(),
		CameraDestinations: runtimeCameraDestinations(),
		CameraPolicy:       runtimeCameraPolicy(),
		DataFields:         runtimeDataFields(),
		Deprecation:        runtimeDeprecationModel(),
		Certification:      runtimeCertificationModel(),
		Preview:            runtimePreviewModel(),
		Ownership: RuntimeOwnership{
			MotionDefinitions: "ChrononTemplate catalog/motion_catalog.v1.json (motion IDs, families, recipes)",
			SemanticContract:  "RenderingGen contracts/overlay-plan.v1.schema.json (semantic items and plan fields)",
			CompositionModel:  "RenderingGen internal/overlay composition catalogs (image, map, background, camera, metric/date)",
			RuntimeCatalog:    "RenderingGen cmd/motion-catalog (compiled view consumed by the picker)",
		},
	}
}

// runtimeDeprecationModel publishes the retirement policy and every motion the
// registry has retired. Registry.List() is sorted, so the projection is stable
// without a second sort.
func runtimeDeprecationModel() RuntimeDeprecationModel {
	model := RuntimeDeprecationModel{
		Policy:         deprecationPolicyImmediate,
		WindowDays:     0,
		AliasesAllowed: false,
		Rule: "a deprecated motion is immediately non-selectable and not resolvable: the picker excludes it from families and use cases, " +
			"and a plan that still names it fails compilation with a diagnostic naming the ID; no alias is published and no similar animation is substituted",
		Deprecated: []RuntimeDeprecationEntry{},
	}
	for _, id := range motion.Registry.List() {
		info, ok := motion.Registry.DeprecationInfo(id)
		if !ok {
			continue
		}
		model.Deprecated = append(model.Deprecated, RuntimeDeprecationEntry{
			MotionID:    id,
			RetiredOn:   strings.TrimSpace(info.RemoveAfter),
			Reason:      strings.TrimSpace(info.Reason),
			Replacement: strings.TrimSpace(info.Replacement),
		})
	}
	return model
}

// runtimeCertificationModel publishes the parsed certification reports. The
// statuses themselves travel per motion; this block states where they come from,
// how fresh they are and what the rule is, so a consumer never has to guess
// whether "passed" describes the current build.
func runtimeCertificationModel() RuntimeCertificationModel {
	model := RuntimeCertificationModel{
		Source:    "renderinggen/motion-certification/latest/*.json (checked-in report snapshots)",
		IsCurrent: false,
		Rule: "status is the newest recorded outcome per motion, from the newest report that names it; " +
			"a motion no report names is unverified and must not be offered as certified; " +
			"failed means a recorded run returned a non-passing status; re-run the certification suite to refresh",
		Reports: []RuntimeCertificationReport{},
	}
	ids := motion.Registry.List()
	snapshot, err := certificationSnapshot()
	if err != nil {
		model.Error = err.Error()
		model.Unverified = len(ids)
		return model
	}
	model.SnapshotAt = snapshot.SnapshotAt
	model.Passed, model.Failed, model.Unverified = snapshot.Counts(ids)
	for _, report := range snapshot.Reports {
		model.Reports = append(model.Reports, RuntimeCertificationReport{
			Report:      report.Name,
			Scope:       report.Scope,
			Backend:     report.Backend,
			GeneratedAt: report.GeneratedAt,
			Motions:     report.Motions,
		})
	}
	return model
}

// runtimePreviewModel publishes the parsed preview ledger. Recorded and None are
// counted over the registry, so the two always sum to the number of motions a
// consumer could show; Entries is the ledger size, which is larger because one
// motion can appear in several galleries.
func runtimePreviewModel() RuntimePreviewModel {
	model := RuntimePreviewModel{
		Source:     "renderinggen/motion-preview/registry.v1.json (ledger written by cmd/preview-registry)",
		MediaInGit: false,
		Rule: "preview is a pointer index over repo-produced gallery artifacts; recorded means an artifact declares the motion, " +
			"not that a clip is present; media is filled only when the gallery names it mechanically; the clips are gitignored and " +
			"must be regenerated from the named artifact",
		Roots: []string{},
	}
	ids := motion.Registry.List()
	registry, err := previewRegistry()
	if err != nil {
		model.Error = err.Error()
		model.None = len(ids)
		return model
	}
	model.GeneratedAt = registry.GeneratedAtUTC
	model.Roots = append([]string(nil), registry.Roots...)
	model.Entries = len(registry.Entries)
	for _, id := range ids {
		if _, ok := registry.EntryFor(id); ok {
			model.Recorded++
		} else {
			model.None++
		}
	}
	return model
}

// backgroundCompositionFor resolves a plan-scope background composition id from
// the same table the compiler validates.
func backgroundCompositionFor(id string) *backgroundCompositionDefinition {
	for index := range backgroundCompositionCatalog {
		if backgroundCompositionCatalog[index].ID == id {
			return &backgroundCompositionCatalog[index]
		}
	}
	return nil
}

// runtimeUseCaseZone maps a use case to its product zone. Text use cases belong
// to no zone of this model; the empty string keeps them out of the picker zones
// without inventing a seventh area.
func runtimeUseCaseZone(id string) string {
	for _, definition := range imageCompositionCatalog {
		if definition.ID == id {
			return definition.Zone
		}
	}
	if definition := mapCompositionFor(id); definition != nil {
		return definition.Zone
	}
	if definition := backgroundCompositionFor(id); definition != nil {
		return definition.Zone
	}
	if dataCompositionFor(id) != nil {
		return ZoneData
	}
	return ""
}

// runtimeCompositionTarget is the motion target a composition selects motions
// for. It is derived from the owning catalog so a counted composition without an
// authored family still publishes the target that family will use.
func runtimeCompositionTarget(id string) string {
	if imageCompositionFor(id) != nil {
		return "image"
	}
	if mapCompositionFor(id) != nil {
		return "map_view"
	}
	if backgroundCompositionFor(id) != nil {
		return "background"
	}
	if target := dataCompositionTarget(id); target != "" {
		return target
	}
	switch id {
	case "entity_caption":
		return "caption"
	case "important_phrase":
		return "important_phrase"
	case "short_important_phrase":
		return "short_phrase"
	}
	return ""
}

func runtimeZones() []RuntimeZone {
	images := make([]string, 0, len(imageCompositionCatalog))
	imagesWithText := make([]string, 0, len(imageCompositionCatalog))
	for _, definition := range imageCompositionCatalog {
		switch definition.Zone {
		case ZoneImagesWithText:
			imagesWithText = append(imagesWithText, definition.ID)
		case ZoneImages:
			images = append(images, definition.ID)
		}
	}
	maps := make([]string, 0, len(mapCompositionCatalog))
	for _, definition := range mapCompositionCatalog {
		if definition.Zone == ZoneMaps {
			maps = append(maps, definition.ID)
		}
	}
	backgrounds := make([]string, 0, len(backgroundCompositionCatalog))
	for _, definition := range backgroundCompositionCatalog {
		if definition.Zone == ZoneBackground {
			backgrounds = append(backgrounds, definition.ID)
		}
	}
	return []RuntimeZone{
		{
			ID: ZoneImages, Label: "Immagini",
			Description:    "Single and counted image compositions without text.",
			Status:         zoneStatusAvailable,
			CompositionIDs: images,
		},
		{
			ID: ZoneImagesWithText, Label: "Immagini con testo",
			Description:    "Counted image compositions whose captions are owned by their image layer.",
			Status:         zoneStatusAvailable,
			CompositionIDs: imagesWithText,
		},
		{
			ID: ZoneMaps, Label: "Mappe",
			Description:    "Georeferenced map compositions that keep their Web Mercator window.",
			Status:         zoneStatusAvailable,
			CompositionIDs: maps,
		},
		{
			ID: ZoneBackground, Label: "Background",
			Description:    "Background plate resolved independently from the foreground overlays.",
			Status:         zoneStatusAvailable,
			CompositionIDs: backgrounds,
		},
		{
			ID: ZoneData, Label: "Dati",
			Description:    "Metric and date/timeline text compositions with their own motion families.",
			Status:         zoneStatusAvailable,
			CompositionIDs: dataCompositionIDs(),
		},
		{
			ID: ZoneCameraRoll, Label: "Camera Roll",
			Description:    "Camera control over the scene, an image or a map viewport.",
			Status:         zoneStatusNoComposition,
			CompositionIDs: []string{},
		},
	}
}

// runtimeCompatibilityMatrix derives one entry per use case. Required
// capabilities are read from the admitted motions' renderer requirements, so the
// cell cannot advertise a capability the renderer does not need.
func runtimeCompatibilityMatrix(useCases []RuntimeAnimationUseCase, definitions map[string]*motion.MotionDefinition) []RuntimeCompatibilityEntry {
	entries := make([]RuntimeCompatibilityEntry, 0, len(useCases))
	for _, useCase := range useCases {
		entry := RuntimeCompatibilityEntry{
			CompositionID: useCase.ID,
			Zone:          runtimeUseCaseZone(useCase.ID),
			MotionTarget:  runtimeCompositionTarget(useCase.ID),
			MotionCount:   len(useCase.MotionIDs),
			Available:     len(useCase.MotionIDs) > 0,
		}
		families := make(map[string]struct{})
		requires3D := false
		requiresCamera := false
		for _, id := range useCase.MotionIDs {
			definition := definitions[id]
			if definition == nil {
				continue
			}
			if definition.Category != "" {
				families[definition.Category] = struct{}{}
			}
			if definition.Requires3D != nil && *definition.Requires3D {
				requires3D = true
			}
			if definition.RequiresCamera != nil && *definition.RequiresCamera {
				requiresCamera = true
			}
		}
		entry.MotionFamilies = sortedSet(families)
		entry.RequiredCapabilities = runtimeRequiredCapabilities(entry.MotionTarget, requires3D, requiresCamera)
		if !entry.Available {
			entry.UnavailableReason = "no dedicated motion family has been authored for this composition yet"
		}
		entries = append(entries, entry)
	}
	return entries
}

// runtimeRequiredCapabilities returns the capability set in a fixed order so the
// matrix is byte-stable across runs.
func runtimeRequiredCapabilities(target string, requires3D, requiresCamera bool) []string {
	capabilities := []string{capability2D}
	if requires3D {
		capabilities = append(capabilities, capability3D)
	}
	if requiresCamera {
		capabilities = append(capabilities, capabilityCamera)
	}
	if target == "map_view" {
		capabilities = append(capabilities, capabilityGeospatial)
	}
	return capabilities
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// runtimeCompositionLayouts publishes the layout contract for every counted
// image composition, derived from the same catalog the compiler validates.
func runtimeCompositionLayouts() []RuntimeCompositionLayout {
	layouts := make([]RuntimeCompositionLayout, 0, len(imageCompositionCatalog))
	for _, definition := range imageCompositionCatalog {
		count := definition.Composition.ImageCount
		activeLayer := compositionActiveLayerSingle
		if count > 1 {
			activeLayer = compositionActiveLayerMulti
		}
		layout := RuntimeCompositionLayout{
			ID:               definition.ID + "_layout",
			CompositionID:    definition.ID,
			Zone:             runtimeUseCaseZone(definition.ID),
			AspectRatio:      compositionAspectRatioRule,
			CropMode:         compositionCropMode,
			SafeAreaFraction: AnchorSafeAreaFraction,
			GapFraction:      compositionGapFraction,
			LayerOrder:       compositionLayerOrder,
			ActiveLayer:      activeLayer,
			MissingAsset:     compositionMissingAsset,
			MotionScope:      compositionMotionScopeItem,
		}
		if count > 1 {
			layout.MotionScope = compositionMotionScopePerLayer
		}
		if definition.Composition.Caption != nil {
			layout.MaxCaptions = definition.Composition.Caption.Maximum
			layout.CaptionEntryOrder = compositionCaptionEntryOrder
			layout.CaptionMotionScope = compositionMotionScopePerCaption
			layout.CaptionUseCase = compositionCaptionUseCase
			layout.TextLimitLines = captionTextLimitLines(definition.Composition.Caption.Maximum)
		}
		layouts = append(layouts, layout)
	}
	return layouts
}

// runtimeMapLayouts declares every map layout and what the lowering supports.
// Route has a closed local raster/path lowering. Stops and callout remain
// explicitly unavailable until their distinct presentation/lifetime contracts exist.
func runtimeMapLayouts() []RuntimeMapLayout {
	unsupported := "no lowering exists for this map layout yet"
	return []RuntimeMapLayout{
		{ID: "basemap", CompositionID: "one_map", Supported: true, Description: "Full-canvas georeferenced raster plate.", ProjectedMotionTargets: []string{"map_view"}},
		{ID: "pins", CompositionID: "one_map", Supported: true, Description: "One grounded shape layer per declared place.", ProjectedMotionTargets: []string{"map_view"}},
		{ID: "pin_labels", CompositionID: "one_map", Supported: true, Description: "One text layer per pin, placed clear of the pin and its neighbours.", ProjectedMotionTargets: []string{"map_view"}},
		{ID: "attribution", CompositionID: "one_map", Supported: true, Description: "Provider-required credit carried by its own text layer.", ProjectedMotionTargets: []string{}},
		{ID: "camera_fly_to", CompositionID: "one_map", Supported: true, Description: "Continues the map plate through camera_move or Natural Earth fly_to_feature resolution instead of a static motion.", ProjectedMotionTargets: []string{"map_view"}},
		{ID: "route", CompositionID: "one_map", Supported: true, Description: "A great-circle path between grounded WGS84 stops, projected into the certified local raster and revealed with the native trim-path operator.", ProjectedMotionTargets: []string{"map_view"}},
		{ID: "stops", CompositionID: "one_map", Supported: false, Description: "An ordered sequence of itinerary stops.", ProjectedMotionTargets: []string{}, UnavailableReason: unsupported},
		{ID: "callout", CompositionID: "one_map", Supported: false, Description: "A connected label box anchored to a place.", ProjectedMotionTargets: []string{}, UnavailableReason: unsupported},
	}
}

// runtimeBackgroundSources derives the supported sources from the background
// composition catalog and appends the sources the lowering still refuses, so
// the picker can show them as unavailable instead of hiding them.
func runtimeBackgroundSources() []RuntimeBackgroundSource {
	sources := make([]RuntimeBackgroundSource, 0, len(backgroundCompositionCatalog)+3)
	for _, definition := range backgroundCompositionCatalog {
		source := RuntimeBackgroundSource{
			Kind: definition.Kind, CompositionID: definition.ID,
			Description: definition.Description, Supported: true, Coverage: "full_canvas",
		}
		switch definition.Kind {
		case "color":
			source.Priority = 0
		case "image":
			source.RequiresAssets = true
			source.FitPolicy = "cover"
			source.Priority = 1
		case "video":
			source.RequiresAssets = true
			source.FitPolicy = "cover"
			source.Loop = true
			source.Priority = 1
		}
		sources = append(sources, source)
	}
	unsupported := "the background lowering accepts only color, image and video today"
	sources = append(sources,
		RuntimeBackgroundSource{Kind: "gradient", Description: "Interpolated color ramp.", Coverage: "full_canvas", Priority: 2, UnavailableReason: unsupported},
		RuntimeBackgroundSource{Kind: "pattern", Description: "Tiled texture.", Coverage: "full_canvas", RequiresAssets: true, Priority: 2, UnavailableReason: unsupported},
		RuntimeBackgroundSource{Kind: "generated", Description: "Renderer-synthesised abstract plate.", Coverage: "full_canvas", Priority: 2, UnavailableReason: unsupported},
	)
	return sources
}
