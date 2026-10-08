package motion

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type RegistryType struct {
	plugins map[string]MotionPlugin
	retired map[string]Deprecation
}

var Registry = NewRegistry()

func NewRegistry() *RegistryType {
	return &RegistryType{plugins: make(map[string]MotionPlugin), retired: make(map[string]Deprecation)}
}

// Deprecation is an explicit registry state, separate from registration: a
// deprecated definition remains resolvable for saved-plan compatibility but is
// not selectable for newly authored picker choices.
type Deprecation struct {
	MotionID    string
	RemoveAfter string
	Reason      string
	Replacement string
}

// MarkDeprecated keeps the motion executable for historic plans while storing
// a deprecation notice. Catalog-source metadata remains the canonical producer
// authority; callers must not use this method to alter production state without
// a reviewed deprecation proposal.
func (r *RegistryType) MarkDeprecated(value Deprecation) error {
	id := strings.TrimSpace(value.MotionID)
	if id == "" || strings.TrimSpace(value.RemoveAfter) == "" || strings.TrimSpace(value.Reason) == "" {
		return fmt.Errorf("motion: deprecation requires motion_id, remove_after and reason")
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(value.RemoveAfter)); err != nil {
		return fmt.Errorf("motion: deprecation remove_after must be YYYY-MM-DD: %w", err)
	}
	if _, ok := r.plugins[id]; !ok {
		return fmt.Errorf("motion: cannot deprecate unknown motion %q", id)
	}
	if replacement := strings.TrimSpace(value.Replacement); replacement != "" {
		if _, ok := r.plugins[replacement]; !ok {
			return fmt.Errorf("motion: deprecation replacement %q is not registered", replacement)
		}
		if replacement == id {
			return fmt.Errorf("motion: deprecation replacement must differ from %q", id)
		}
	}
	if _, exists := r.retired[id]; exists {
		return fmt.Errorf("motion: deprecation for %q is already registered", id)
	}
	value.MotionID = id
	value.RemoveAfter = strings.TrimSpace(value.RemoveAfter)
	value.Reason = strings.TrimSpace(value.Reason)
	value.Replacement = strings.TrimSpace(value.Replacement)
	r.retired[id] = value
	return nil
}

// DeprecationReason returns the reason that makes this ID unavailable for new
// selection. It does not affect Resolve: legacy saved plans still compile.
func (r *RegistryType) DeprecationReason(id string) (string, bool) {
	value, ok := r.retired[strings.TrimSpace(id)]
	return value.Reason, ok
}

// DeprecationInfo returns the registered deprecation schedule, if present.
func (r *RegistryType) DeprecationInfo(id string) (Deprecation, bool) {
	value, ok := r.retired[strings.TrimSpace(id)]
	return value, ok
}

// Selectable reports whether the ID resolves and is not deprecated.
func (r *RegistryType) Selectable(id string) bool {
	id = strings.TrimSpace(id)
	if _, deprecated := r.retired[id]; deprecated {
		return false
	}
	_, err := r.Resolve(id)
	return err == nil && id != ""
}

// SelectableCategoryMotionIDs is the picker projection; CategoryMotionIDs
// remains the compatibility projection for saved plans and existing consumers.
func (r *RegistryType) SelectableCategoryMotionIDs(category string) []string {
	ids := r.CategoryMotionIDs(category)
	selectable := ids[:0]
	for _, id := range ids {
		if r.Selectable(id) {
			selectable = append(selectable, id)
		}
	}
	return selectable
}

func (r *RegistryType) Register(id string, plugin MotionPlugin) error {
	id = strings.TrimSpace(id)
	if id == "" || plugin == nil || plugin.ID() != id {
		return fmt.Errorf("motion: invalid plugin registration %q", id)
	}
	if _, exists := r.plugins[id]; exists {
		return fmt.Errorf("motion: plugin %q already registered", id)
	}
	r.plugins[id] = plugin
	return nil
}

func (r *RegistryType) Resolve(id string) (MotionPlugin, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	p, ok := r.plugins[id]
	if !ok {
		return nil, fmt.Errorf("motion: unsupported plugin %q", id)
	}
	return p, nil
}

func (r *RegistryType) List() []string {
	ids := make([]string, 0, len(r.plugins))
	for id := range r.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Categories returns every motion category present in the registry in stable
// order. It exists so classification decisions (phrase-planning pool,
// preview gating) are audited against the actual catalog instead of a
// remembered list: a new category must be classified explicitly, or the
// classification test fails.
func (r *RegistryType) Categories() []string {
	seen := map[string]struct{}{}
	categories := []string{}
	for _, id := range r.List() {
		plugin, err := r.Resolve(id)
		if err != nil {
			continue
		}
		declarative, ok := plugin.(DeclarativePlugin)
		if !ok {
			continue
		}
		category := strings.TrimSpace(declarative.Definition.Category)
		if category == "" {
			continue
		}
		if _, ok := seen[category]; !ok {
			seen[category] = struct{}{}
			categories = append(categories, category)
		}
	}
	sort.Strings(categories)
	return categories
}

// phrasePlanningCategories is the ONE explicit list of families that feed the
// seeded phrase-planning pool. Categories that are selectable elsewhere but
// deliberately excluded here (preview-gated families) live in
// previewGatedCategories with their reason, so the exclusion is a decision,
// not an omission. TestPhrasePoolClassificationIsExhaustive fails when a new
// catalog category appears in neither list.
var phrasePlanningCategories = [...]string{
	"typewriter", "apple_v2", "apple_v3", "phrase_apple_clean_v1", "apple_phrase_v1", "brush_v1", "text_3d_v1",
}

// previewGatedCategories are selectable through the runtime catalog but stay
// out of the seeded legacy pool: reason documents why.
var previewGatedCategories = map[string]string{
	"typewriter_modern_v1": "separately selectable preview category, exposed independently through CategoryMotionIDs",
}

// phrasePoolExcludedCategories admit planning-relevant targets but are
// deliberately out of the seeded pool: each has its own selection surface.
// Listed here with reasons so the exclusion stays a decision: the historical
// pool bytes are unchanged, and a future family cannot slip into this state
// silently.
var phrasePoolExcludedCategories = map[string]string{
	"short_phrase_style":   "selected through the short-phrase style catalog, not the legacy phrase pool",
	"trump_entity_text_v1": "entity-text family selected through entity surfaces, not the legacy phrase pool",
	"entity_caption_v1":    "caption family selected through the caption use case, not the legacy phrase pool",
	"entity_card_v1":       "entity-card family selected through entity surfaces, not the legacy phrase pool",
	"metric_v1":            "metric family selected through the metric_stat composition, not the legacy phrase pool",
	"date_v1":              "date family selected through the timeline_date composition, not the legacy phrase pool",
	"metric_didone_v1":     "Didone metric family selected through the metric_stat composition, not the legacy phrase pool",
	"date_didone_v1":       "Didone date family selected through the timeline_date composition, not the legacy phrase pool",
	"image_25d_clean_v1":   "image-surface family, never a phrase-planning family",
	"editorial_image_v1":   "image-surface family, never a phrase-planning family",
	"image_premium_v1":     "image-surface family, never a phrase-planning family",
	"overlay_v3_image":     "image-surface family, never a phrase-planning family",
	"light_leak_v1":        "image-surface family, never a phrase-planning family",
	"paint_v1":             "image-surface family, never a phrase-planning family",
	"web":                  "image-surface family, never a phrase-planning family",
	"web_rect_v1":          "image-surface family, never a phrase-planning family",
	"map_image_v1":         "map-surface family, never a phrase-planning family",
}

// PhraseAnimationIDs returns every motion in the established phrase-planning
// families, in stable order. The separately selectable typewriter_modern_v1
// preview category stays out of the seeded legacy pool; the runtime catalog
// exposes it independently through CategoryMotionIDs.
func (r *RegistryType) PhraseAnimationIDs() []string {
	ids := make([]string, 0)
	for _, category := range phrasePlanningCategories {
		ids = append(ids, r.CategoryMotionIDs(category)...)
	}
	sort.Strings(ids)
	return compactMotionIDs(ids)
}

// ImageOverlayMotionIDs returns the complete independently selectable image
// motion inventory: Overlay V3 layer motions plus clean 2.5D image motions.
func (r *RegistryType) ImageOverlayMotionIDs() []string {
	ids := append(r.CategoryMotionIDs("overlay_v3_image"), r.CategoryMotionIDs("image_25d_clean_v1")...)
	sort.Strings(ids)
	return ids
}

var visualAccentsV1CategoryIDs = [...]string{"brush_v1", "web_rect_v1", "paint_v1", "light_leak_v1"}

// VisualAccentsV1Categories is the stable public list of the four official
// Visual Accents V1 families. Validation uses the private source so callers
// cannot mutate the category allowlist through this compatibility slice.
var VisualAccentsV1Categories = append([]string(nil), visualAccentsV1CategoryIDs[:]...)

// VisualAccentsV1CategoriesContains reports whether category is one of the
// four official Visual Accents V1 families.
func VisualAccentsV1CategoriesContains(category string) bool {
	for _, family := range visualAccentsV1CategoryIDs {
		if family == category {
			return true
		}
	}
	return false
}

// CategoryMotionIDs returns registered declarative motions in one catalog
// category. It is the single projection used by certification and manifests.
func (r *RegistryType) CategoryMotionIDs(category string) []string {
	ids := make([]string, 0)
	for _, id := range r.List() {
		plugin, err := r.Resolve(id)
		if err != nil {
			continue
		}
		declarative, ok := plugin.(DeclarativePlugin)
		if !ok {
			continue
		}
		definition := declarative.Definition
		if definition.Category == category {
			ids = append(ids, id)
		}
	}
	return ids
}

// IsCameraBacked3DProperty is RenderingGen's single authority for the closed set
// of layer properties that require Chronon's camera-backed render path. It
// mirrors Chronon3d's render_plan_decoder.cpp:is_3d_property exactly: Z
// translation and X/Y rotation opt in, while scale_z and in-plane rotation_z do
// not. Both motion validation and overlay compiler enable_3d routing delegate
// here, so the producer and consumer share one property list.
func IsCameraBacked3DProperty(property string) bool {
	switch property {
	case "position_z", "rotation_x", "rotation_y":
		return true
	default:
		return false
	}
}

func motionHas3D(definition MotionDefinition) bool {
	for _, track := range definition.Tracks {
		if IsCameraBacked3DProperty(track.Property) {
			return true
		}
	}
	for _, animator := range definition.TextAnimators {
		for _, track := range animator.Properties {
			if IsCameraBacked3DProperty(track.Property) {
				return true
			}
		}
	}
	return false
}

func compactMotionIDs(ids []string) []string {
	if len(ids) < 2 {
		return ids
	}
	write := 1
	for read := 1; read < len(ids); read++ {
		if ids[read] == ids[write-1] {
			continue
		}
		ids[write] = ids[read]
		write++
	}
	return ids[:write]
}
