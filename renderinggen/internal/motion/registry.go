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

func Register(id string, plugin MotionPlugin) error { return Registry.Register(id, plugin) }
func Resolve(id string) (MotionPlugin, error)       { return Registry.Resolve(id) }
func List() []string                                { return Registry.List() }
