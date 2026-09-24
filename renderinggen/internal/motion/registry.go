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
		return strings.HasPrefix(definition.ID, "typewriter_")
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
	return []string{"typewriter", "classic_apple", "modern_apple", "web", "3d"}
}

// FamilyMotionIDs returns the registered motion ids for one public family.
func (r *RegistryType) FamilyMotionIDs(family string) []string {
	return r.CategoryMotionIDs(family)
}
