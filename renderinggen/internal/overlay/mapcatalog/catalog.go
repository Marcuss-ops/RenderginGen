// Package mapcatalog owns the embedded ChrononPlugin map-renderer catalog and
// its strict validation. It is a leaf: it imports only the standard library,
// and the overlay root imports it, never the other way around.
package mapcatalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

//go:embed catalog/chrononplugin_maps_renderer_catalog.v1.json
var embeddedJSON []byte

const (
	schema          = "chrononplugin.maps-renderer-catalog.v1"
	version         = 1
	source          = "ChrononPlugin"
	rendererBinary  = "maps_dynamic_map_render"
	telemetrySchema = "chronon.dynamic-map-telemetry.v1"

	// FileName is the embedded projection's filename, used by the drift test's
	// actionable error message.
	FileName = "chrononplugin_maps_renderer_catalog.v1.json"
)

// Catalog is the exact public shape of the ChrononPlugin catalog. Unknown
// fields are rejected by Load: a newer catalog is a build-ordering mistake, not
// something to best-effort parse.
type Catalog struct {
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

var embedded = mustLoad(embeddedJSON)

// Embedded returns the validated catalog compiled into the worker. The value
// is initialized once; callers must treat it as read-only.
func Embedded() Catalog { return embedded }

// Load decodes and validates one catalog document.
func Load(raw []byte) (Catalog, error) {
	var catalog Catalog
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
	if catalog.Schema != schema {
		return catalog, fmt.Errorf("schema %q, want %q", catalog.Schema, schema)
	}
	if catalog.Version != version {
		return catalog, fmt.Errorf("version %d, want %d", catalog.Version, version)
	}
	if catalog.Source != source {
		return catalog, fmt.Errorf("source %q, want %q", catalog.Source, source)
	}
	if catalog.Renderer.Binary != rendererBinary {
		return catalog, fmt.Errorf("renderer.binary %q, want %q", catalog.Renderer.Binary, rendererBinary)
	}
	if catalog.Renderer.TelemetrySchema != telemetrySchema {
		return catalog, fmt.Errorf("renderer.telemetry_schema %q, want %q", catalog.Renderer.TelemetrySchema, telemetrySchema)
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

// Set projects a catalog list into the membership set the overlay compiler
// validates against.
func Set(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func mustLoad(raw []byte) Catalog {
	catalog, err := Load(raw)
	if err != nil {
		panic(fmt.Sprintf("mapcatalog: embedded ChrononPlugin map catalog: %v", err))
	}
	return catalog
}
