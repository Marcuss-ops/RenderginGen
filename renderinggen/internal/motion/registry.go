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

// AppleV2MotionIDs returns the registered Apple V2 motion IDs in stable order.
// Styles are intentionally not part of this list: callers select one visual
// preset and choose one of these motions independently through motion_id.
func (r *RegistryType) AppleV2MotionIDs() []string {
	return r.CategoryMotionIDs("apple_v2")
}

// AppleV3MotionIDs returns the complete Apple-like Overlay V3 text vocabulary
// in stable order. The category is read from the canonical ChrononTemplate
// catalog; this package never maintains a second list of ids.
func (r *RegistryType) AppleV3MotionIDs() []string {
	return r.CategoryMotionIDs("apple_v3")
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
		if ok && declarative.Definition.Category == category {
			ids = append(ids, id)
		}
	}
	return ids
}
