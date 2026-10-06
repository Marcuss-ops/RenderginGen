package motion

import (
	"fmt"
	"sort"
	"strings"
)

type RegistryType struct{ plugins map[string]MotionPlugin }

var Registry = NewRegistry()

func NewRegistry() *RegistryType { return &RegistryType{plugins: make(map[string]MotionPlugin)} }

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

// AppleV2MotionIDs returns the registered classic Apple motion IDs in stable
// order. Styles are intentionally independent: callers select the sole phrase
// preset and choose one of these motions through motion_id.
func (r *RegistryType) AppleV2MotionIDs() []string {
	return r.CategoryMotionIDs("apple_v2")
}

// AppleV3MotionIDs returns the canonical Apple V3 text vocabulary in stable
// order. The category is read from the canonical ChrononTemplate catalog; this
// package never maintains a second list of ids.
func (r *RegistryType) AppleV3MotionIDs() []string {
	return r.CategoryMotionIDs("apple_v3")
}

// ApplePhrasePackMotionIDs returns the standalone Modern Apple phrase pack.
func (r *RegistryType) ApplePhrasePackMotionIDs() []string {
	return r.CategoryMotionIDs("apple_phrase_v1")
}

// PhraseAnimationIDs returns every motion in the established phrase-planning
// families, in stable order. The separately selectable typewriter_modern_v1
// preview family stays out of the seeded legacy pool; callers can request that
// family explicitly through FamilyMotionIDs.
func (r *RegistryType) PhraseAnimationIDs() []string {
	ids := make([]string, 0)
	for _, family := range []string{"typewriter", "classic_apple", "modern_apple", "brush_v1", "text_3d_v1"} {
		ids = append(ids, r.FamilyMotionIDs(family)...)
	}
	sort.Strings(ids)
	return compactMotionIDs(ids)
}

// ImageV3MotionIDs returns the complete Overlay V3 image vocabulary in stable
// order. Image motions remain layer-level so they can use the same 2.5D
// position/scale/rotation contract as text without inventing a second engine.
func (r *RegistryType) ImageV3MotionIDs() []string {
	return r.CategoryMotionIDs("overlay_v3_image")
}

// Image25DCleanV1MotionIDs returns the catalog-owned, layer-only clean 2.5D
// image motions. Camera-driven recipes are intentionally excluded because
// they move the whole source composition rather than one overlay layer.
func (r *RegistryType) Image25DCleanV1MotionIDs() []string {
	return r.CategoryMotionIDs("image_25d_clean_v1")
}

// ImageOverlayMotionIDs returns the complete independently selectable image
// motion inventory: Overlay V3 layer motions plus clean 2.5D image motions.
func (r *RegistryType) ImageOverlayMotionIDs() []string {
	ids := append(r.ImageV3MotionIDs(), r.Image25DCleanV1MotionIDs()...)
	sort.Strings(ids)
	return ids
}

// MapImageV1MotionIDs returns the ChrononTemplate map-image recipe family.
// These motions are admitted only by the semantic map compiler, where their
// shared scale track is also projected onto grounded pins and labels.
func (r *RegistryType) MapImageV1MotionIDs() []string {
	return r.CategoryMotionIDs("map_image_v1")
}

// EditorialImageV1MotionIDs returns the Editorial Visual Motion V1 image
// vocabulary in stable order — the 14 goal motions with the full resting-pose
// and duration-bounds metadata the V1 contract certifies.
func (r *RegistryType) EditorialImageV1MotionIDs() []string {
	return r.CategoryMotionIDs("editorial_image_v1")
}

// ImagePremiumV1MotionIDs returns the data-driven rounded-frame image recipes.
// This dedicated category is intentionally separate from the legacy 18-ID
// overlay inventory and its certified batch matrix.
func (r *RegistryType) ImagePremiumV1MotionIDs() []string {
	return r.CategoryMotionIDs("image_premium_v1")
}

// Text3DV1MotionIDs returns the Editorial Visual Motion V1 text 2.5D/3D
// vocabulary in stable order: 10 motions carrying camera-backed transforms.
func (r *RegistryType) Text3DV1MotionIDs() []string {
	return r.CategoryMotionIDs("text_3d_v1")
}

// EntityCaptionV1MotionIDs returns the animated-caption vocabulary for entity
// cards in stable order.
func (r *RegistryType) EntityCaptionV1MotionIDs() []string {
	return r.CategoryMotionIDs("entity_caption_v1")
}

// TrumpEntityTextV1MotionIDs returns the premium Trump entity-caption styles.
func (r *RegistryType) TrumpEntityTextV1MotionIDs() []string {
	return r.CategoryMotionIDs("trump_entity_text_v1")
}

// VisualAccentsV1MotionIDs returns one official Visual Accents V1 family
// (brush_v1, web_rect_v1, paint_v1, light_leak_v1) in stable order. Each
// family is a closed 12-motion vocabulary certified by its own gate.
func (r *RegistryType) VisualAccentsV1MotionIDs(category string) []string {
	return r.CategoryMotionIDs(category)
}

// VisualAccentsV1FamilyMotionIDs returns every registered Visual Accents V1
// motion across the four official families, sorted.
func (r *RegistryType) VisualAccentsV1FamilyMotionIDs() []string {
	ids := make([]string, 0, 59)
	for _, category := range VisualAccentsV1Categories {
		ids = append(ids, r.CategoryMotionIDs(category)...)
	}
	sort.Strings(ids)
	return ids
}

// VisualAccentsV1Categories is the stable public list of the four official
// Visual Accents V1 families.
var VisualAccentsV1Categories = []string{"brush_v1", "web_rect_v1", "paint_v1", "light_leak_v1"}

// VisualAccentsV1CategoriesContains reports whether category is one of the
// four official Visual Accents V1 families.
func VisualAccentsV1CategoriesContains(category string) bool {
	for _, family := range VisualAccentsV1Categories {
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
		if definition.Category == category || belongsToFamily(definition, category) {
			ids = append(ids, id)
		}
	}
	return ids
}

func belongsToFamily(definition MotionDefinition, family string) bool {
	if family == "typewriter" {
		return strings.HasPrefix(definition.ID, "typewriter_") && definition.Category != "typewriter_modern_v1"
	}
	if family == "typewriter_modern_v1" {
		return definition.Category == "typewriter_modern_v1"
	}
	if family == "classic_apple" && definition.Category == "apple_v2" {
		return true
	}
	if family == "modern_apple" && (definition.Category == "apple_v3" || definition.Category == "phrase_apple_clean_v1" || definition.Category == "apple_phrase_v1") {
		return true
	}
	if family == "web" && (definition.Category == "web" || definition.Category == "web_motion") {
		return true
	}
	return family == "3d" && motionHas3D(definition)
}

// IsCameraBacked3DProperty is RenderingGen's single authority for the closed set
// of layer properties that require Chronon's camera-backed render path. It
// mirrors Chronon3d's render_plan_decoder.cpp:is_3d_property exactly: Z
// translation and X/Y rotation opt in, while scale_z and in-plane rotation_z do
// not. Every RenderingGen caller — the motion registry's 3d family and the
// overlay compiler's enable_3d routing — delegates here, so the producer and
// the consumer can only disagree if this list changes without Chronon.
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

// MotionFamilies is the stable public vocabulary for independently selectable
// motion groups. A group can be intentionally empty (for example web) without
// creating a fake or non-renderable motion.
func (r *RegistryType) MotionFamilies() []string {
	return []string{"phrase", "short_phrase_style", "typewriter", "typewriter_modern_v1", "classic_apple", "modern_apple", "brush_v1", "text_3d_v1", "trump_entity_text_v1", "web", "3d"}
}

// FamilyMotionIDs returns the registered motion ids for one public family.
func (r *RegistryType) FamilyMotionIDs(family string) []string {
	if family == "phrase" {
		return r.PhraseAnimationIDs()
	}
	if family == "short_phrase_style" {
		return r.ShortPhraseStyleIDs()
	}
	if family == "brush_v1" {
		return r.VisualAccentsV1MotionIDs(family)
	}
	ids := r.CategoryMotionIDs(family)
	if family == "3d" {
		// Text 3D is a named V1 subfamily as well as part of the public 3D
		// capability family. Unioning it here keeps the dedicated catalog view
		// tied to the same renderable family API.
		ids = append(ids, r.Text3DV1MotionIDs()...)
		sort.Strings(ids)
		ids = compactMotionIDs(ids)
	}
	return ids
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
