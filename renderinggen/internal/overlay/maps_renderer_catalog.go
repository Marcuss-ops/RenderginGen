// maps_renderer_catalog.go owns the map presentation vocabulary RenderingGen
// validates against. The vocabulary is NOT owned here: ChrononPlugin publishes
// it as `plugins/maps/maps-renderer-catalog.v1.json`, and
// `catalog/chrononplugin_maps_renderer_catalog.v1.json` is the embedded
// projection of that file. A drift test fails when the two disagree, so a map
// composition the native renderer refuses can never be admitted here (and a
// composition it draws can never be rejected because this file went stale).
package overlay

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

//go:embed catalog/chrononplugin_maps_renderer_catalog.v1.json
var chrononPluginMapsCatalogJSON []byte

const (
	chrononPluginMapsCatalogSchema   = "chrononplugin.maps-renderer-catalog.v1"
	chrononPluginMapsCatalogVersion  = 1
	chrononPluginMapsCatalogSource   = "ChrononPlugin"
	chrononPluginMapsRendererBinary  = "maps_dynamic_map_render"
	chrononPluginMapsTelemetrySchema = "chronon.dynamic-map-telemetry.v1"
	chrononPluginMapsCatalogFileName = "chrononplugin_maps_renderer_catalog.v1.json"
)

// chrononPluginMapsRendererCatalog is the exact public shape of the catalog.
// Unknown fields are rejected: a newer catalog is a build-ordering mistake, not
// something to best-effort parse.
type chrononPluginMapsRendererCatalog struct {
	Schema   string `json:"schema"`
	Version  int    `json:"version"`
	Source   string `json:"source"`
	Renderer struct {
		Binary          string `json:"binary"`
		TelemetrySchema string `json:"telemetry_schema"`
	} `json:"renderer"`
	BaseFlyover      string   `json:"base_flyover"`
	MapAnimations    []string `json:"map_animations"`
	CameraAnimations []string `json:"camera_animations"`
	LabelAnimations  []string `json:"label_animations"`
	Basemaps         []string `json:"basemaps"`
}

var (
	chrononPluginMaps = mustLoadChrononPluginMapsCatalog(chrononPluginMapsCatalogJSON)

	// Map presentation vocabulary, projected from the ChrononPlugin catalog.
	// These replace the former hand-maintained lists (the retired Python
	// composition families region_zoom/pulse_marker/spotlight/ed_*), which
	// described a renderer that no longer exists.
	certifiedMapCompositions     = chrononPluginMaps.stringSet(chrononPluginMaps.MapAnimations)
	certifiedMapCameraAnimations = chrononPluginMaps.stringSet(chrononPluginMaps.CameraAnimations)
	certifiedMapLabelAnimations  = chrononPluginMaps.stringSet(chrononPluginMaps.LabelAnimations)
)

func mustLoadChrononPluginMapsCatalog(raw []byte) chrononPluginMapsRendererCatalog {
	catalog, err := loadChrononPluginMapsCatalog(raw)
	if err != nil {
		panic(fmt.Sprintf("overlay: embedded ChrononPlugin map catalog: %v", err))
	}
	return catalog
}

func loadChrononPluginMapsCatalog(raw []byte) (chrononPluginMapsRendererCatalog, error) {
	var catalog chrononPluginMapsRendererCatalog
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return catalog, fmt.Errorf("decode: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return catalog, fmt.Errorf("contains trailing JSON")
	} else if err != io.EOF {
		return catalog, fmt.Errorf("decode trailing data: %w", err)
	}
	if catalog.Schema != chrononPluginMapsCatalogSchema {
		return catalog, fmt.Errorf("schema %q, want %q", catalog.Schema, chrononPluginMapsCatalogSchema)
	}
	if catalog.Version != chrononPluginMapsCatalogVersion {
		return catalog, fmt.Errorf("version %d, want %d", catalog.Version, chrononPluginMapsCatalogVersion)
	}
	if catalog.Source != chrononPluginMapsCatalogSource {
		return catalog, fmt.Errorf("source %q, want %q", catalog.Source, chrononPluginMapsCatalogSource)
	}
	if catalog.Renderer.Binary != chrononPluginMapsRendererBinary {
		return catalog, fmt.Errorf("renderer.binary %q, want %q", catalog.Renderer.Binary, chrononPluginMapsRendererBinary)
	}
	if catalog.Renderer.TelemetrySchema != chrononPluginMapsTelemetrySchema {
		return catalog, fmt.Errorf("renderer.telemetry_schema %q, want %q", catalog.Renderer.TelemetrySchema, chrononPluginMapsTelemetrySchema)
	}
	if catalog.BaseFlyover != "" {
		return catalog, fmt.Errorf("base_flyover must be the empty composition id")
	}
	lists := []struct {
		name   string
		values []string
	}{
		{"map_animations", catalog.MapAnimations},
		{"camera_animations", catalog.CameraAnimations},
		{"label_animations", catalog.LabelAnimations},
		{"basemaps", catalog.Basemaps},
	}
	for _, list := range lists {
		if len(list.values) == 0 {
			return catalog, fmt.Errorf("%s must not be empty", list.name)
		}
		seen := make(map[string]struct{}, len(list.values))
		for _, value := range list.values {
			if value == "" || strings.TrimSpace(value) != value {
				return catalog, fmt.Errorf("%s contains a blank or untrimmed id %q", list.name, value)
			}
			if _, duplicate := seen[value]; duplicate {
				return catalog, fmt.Errorf("%s contains duplicate id %q", list.name, value)
			}
			seen[value] = struct{}{}
		}
	}
	return catalog, nil
}

func (c chrononPluginMapsRendererCatalog) stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
