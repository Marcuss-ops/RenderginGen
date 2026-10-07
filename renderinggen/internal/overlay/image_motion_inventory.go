package overlay

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// RuntimeMotionOption is the renderer-facing, read-only description of one
// registered animation choice. Targets are copied from the canonical motion
// catalog when declared; a missing target list is reported as undeclared, not
// guessed from the ID or category. The compiler remains the compatibility
// authority when legacy catalog entries omit targets.
type RuntimeMotionOption struct {
	ID              string                 `json:"id"`
	Family          string                 `json:"family,omitempty"`
	Targets         []string               `json:"targets,omitempty"`
	TargetsDeclared bool                   `json:"targets_declared"`
	EnterFrames     int                    `json:"enter_frames,omitempty"`
	ExitFrames      int                    `json:"exit_frames,omitempty"`
	DurationBounds  *motion.DurationBounds `json:"duration_bounds,omitempty"`
	Requires3D      bool                   `json:"requires_3d,omitempty"`
	RequiresCamera  bool                   `json:"requires_camera,omitempty"`
}

// runtimeMotionCatalog returns all selectable motions in stable ID order,
// including text/phrase, image, visual-accent, presentation and short-phrase
// styles. Family is the canonical catalog category; use Targets only when
// TargetsDeclared is true. motion_id remains independent from preset_id.
func RuntimeMotionCatalog() []RuntimeMotionOption {
	ids := motion.Registry.List()
	options := make([]RuntimeMotionOption, 0, len(ids))
	for _, id := range ids {
		option := RuntimeMotionOption{ID: id}
		plugin, err := motion.Registry.Resolve(id)
		if err == nil && plugin != nil {
			if declarative, ok := plugin.(motion.DeclarativePlugin); ok {
				definition := declarative.Definition
				option.Family = definition.Category
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
		}
		options = append(options, option)
	}
	return options
}

// runtimeMotionFamilies groups the runtime catalog by its authored canonical
// family/category. It is additive to the narrower legacy family and image
// inventory helpers below, so consumers can offer one family-based picker.
func RuntimeMotionFamilies() map[string][]RuntimeMotionOption {
	families := make(map[string][]RuntimeMotionOption)
	for _, option := range RuntimeMotionCatalog() {
		family := option.Family
		if family == "" {
			family = "uncategorized"
		}
		families[family] = append(families[family], option)
	}
	return families
}

// RuntimeMotionFamilyIDs returns catalog categories in deterministic order.
func RuntimeMotionFamilyIDs() []string {
	families := RuntimeMotionFamilies()
	ids := make([]string, 0, len(families))
	for id := range families {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// RuntimeMotionFamily returns the selectable motions in one canonical family.
// The result is copied from the runtime inventory and stable by motion ID.
func RuntimeMotionFamily(id string) []RuntimeMotionOption {
	return cloneRuntimeMotionOptions(RuntimeMotionFamilies()[id])
}

// RuntimeAnimationUseCase describes a selectable family at the semantic-item
// level. MotionIDs lists only choices exposed for that usage; item-level
// motion_id selects one choice, while image_layers let every child select one
// independently.
type RuntimeAnimationUseCase struct {
	ID          string   `json:"id"`
	ItemKinds   []string `json:"item_kinds"`
	Cardinality string   `json:"cardinality"`
	Description string   `json:"description"`
	MotionIDs   []string `json:"motion_ids"`
}

// RuntimeAnimationUseCases builds practical picker groups matching semantic
// compiler paths, from canonical targets and explicit legacy fallbacks.
func RuntimeAnimationUseCases() []RuntimeAnimationUseCase {
	allOptions := RuntimeMotionCatalog()
	shortPhrases := uniqueSortedMotionIDs(appendMotionIDs(nil,
		motion.Registry.FamilyMotionIDs("short_phrase_style"),
		motion.Registry.FamilyMotionIDs("typewriter_modern_v1"),
	))
	shortPhraseIDs := make(map[string]bool, len(shortPhrases))
	for _, id := range shortPhrases {
		shortPhraseIDs[id] = true
	}
	images := make(map[string]bool)
	for _, ids := range ImageMotionInventory() {
		for _, id := range ids {
			images[id] = true
		}
	}
	imageIDs := sortedMotionSet(images)
	phrases := make(map[string]bool)
	captions := make(map[string]bool)
	for _, option := range allOptions {
		if motionAdmitsTarget(option.ID, "important_phrase") {
			phrases[option.ID] = true
		}
		if motionAdmitsTarget(option.ID, "caption") {
			captions[option.ID] = true
		}
	}
	// Keep short phrases as a distinct use case if a future canonical target
	// declaration also makes them eligible for the general phrase target.
	phraseIDs := make([]string, 0, len(phrases))
	for id := range phrases {
		if !shortPhraseIDs[id] {
			phraseIDs = append(phraseIDs, id)
		}
	}
	phraseIDs = uniqueSortedMotionIDs(phraseIDs)
	captionIDs := sortedMotionSet(captions)
	stackMotions := make(map[string]bool)
	for _, option := range allOptions {
		if animationIsImageStack(option.ID) {
			stackMotions[option.ID] = true
		}
	}
	return []RuntimeAnimationUseCase{
		{ID: "important_phrase", ItemKinds: []string{"important_phrase"}, Cardinality: "one text layer", Description: "Editorial phrase motion; choose a text preset separately.", MotionIDs: phraseIDs},
		{ID: "short_important_phrase", ItemKinds: []string{"important_phrase"}, Cardinality: "one text layer", Description: "Short-phrase styles and modern typewriter motions; choose a text preset separately.", MotionIDs: shortPhrases},
		{ID: "single_image", ItemKinds: []string{"entity_image", "image", "image_popup", "product", "logo"}, Cardinality: "one image", Description: "One asset on the semantic item; motion_id and motion_params are on the item.", MotionIDs: imageIDs},
		{ID: "composite_image_layer", ItemKinds: []string{"entity_image", "image", "image_popup", "product", "logo"}, Cardinality: "two or more image_layers", Description: "Each child image_layer owns its asset, relative time, preset_id, motion_id and motion_params.", MotionIDs: imageIDs},
		{ID: "image_stack", ItemKinds: []string{"image", "entity_image"}, Cardinality: "two or more image_layers", Description: "Stack recipes coordinate the item-level motion across layers and require an active layer selector.", MotionIDs: sortedMotionSet(stackMotions)},
		{ID: "entity_caption", ItemKinds: []string{"entity_image"}, Cardinality: "one caption per image or composite child", Description: "Caption motion is selected separately from the image motion.", MotionIDs: captionIDs},
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func appendMotionIDs(dst []string, groups ...[]string) []string {
	for _, group := range groups {
		dst = append(dst, group...)
	}
	return dst
}

func sortedMotionSet(set map[string]bool) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func uniqueSortedMotionIDs(ids []string) []string {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return sortedMotionSet(set)
}

// cloneRuntimeMotionOptions prevents catalog callers from mutating inventory
// data shared with another API result.
func cloneRuntimeMotionOptions(options []RuntimeMotionOption) []RuntimeMotionOption {
	cloned := make([]RuntimeMotionOption, len(options))
	for i, option := range options {
		cloned[i] = option
		cloned[i].Targets = append([]string(nil), option.Targets...)
		if option.DurationBounds != nil {
			bounds := *option.DurationBounds
			cloned[i].DurationBounds = &bounds
		}
	}
	return cloned
}

// ImageMotionInventory returns registered image-target motions grouped by
// their canonical catalog category. Declared targets are authoritative; the
// sole targetless image category is retained as a compatibility fallback.
func ImageMotionInventory() map[string][]string {
	inventory := make(map[string][]string)
	for _, id := range motion.Registry.List() {
		plugin, err := motion.Registry.Resolve(id)
		if err != nil || plugin == nil {
			continue
		}
		declarative, ok := plugin.(motion.DeclarativePlugin)
		if !ok {
			continue
		}
		definition := declarative.Definition
		if motionAdmitsTarget(id, "image") {
			inventory[definition.Category] = append(inventory[definition.Category], id)
		}
	}
	return inventory
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
}

// CompiledRuntimeAnimationCatalog returns a deterministic snapshot suitable
// for JSON serialization and external UI consumption.
func CompiledRuntimeAnimationCatalog() RuntimeAnimationCatalog {
	return RuntimeAnimationCatalog{
		SchemaVersion: 1,
		Families:      RuntimeMotionFamilyIDs(),
		Motions:       RuntimeMotionCatalog(),
		UseCases:      RuntimeAnimationUseCases(),
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

// PresentationMotionInventory returns Date, Metric and entity-card animation
// IDs from the canonical ChrononTemplate presentation catalog. Each ID is
// selected on a semantic overlay item with motion_id; it is independent of
// the item's text preset_id.
func PresentationMotionInventory() map[string][]string {
	inventory := make(map[string][]string)
	for _, family := range motion.Registry.PresentationFamilyIDs() {
		inventory[family] = motion.Registry.PresentationMotionIDs(family)
	}
	return inventory
}
