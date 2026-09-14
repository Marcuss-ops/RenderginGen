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
	ids := make([]string, 0)
	for _, id := range r.List() {
		plugin, err := r.Resolve(id)
		if err != nil {
			continue
		}
		declarative, ok := plugin.(DeclarativePlugin)
		if ok && declarative.Definition.Category == "apple_v2" {
			ids = append(ids, id)
		}
	}
	return ids
}
