package mapcatalog

import (
	"strings"
	"testing"
)

const validCatalog = `{
	"schema": "chrononplugin.maps-renderer-catalog.v1",
	"version": 1,
	"source": "ChrononPlugin",
	"renderer": {
		"binary": "maps_dynamic_map_render",
		"telemetry_schema": "chronon.dynamic-map-telemetry.v1"
	},
	"base_flyover": "",
	"map_animations": ["route_line"],
	"camera_animations": ["slow_approach"],
	"label_animations": ["none"],
	"basemaps": ["satellite"]
}`

func TestLoadAcceptsValidCatalog(t *testing.T) {
	catalog, err := Load([]byte(validCatalog))
	if err != nil {
		t.Fatalf("Load(valid catalog): %v", err)
	}
	if catalog.Schema != schema || catalog.Version != version || catalog.Source != source {
		t.Fatalf("unexpected identity: %+v", catalog)
	}
	if catalog.Renderer.Binary != rendererBinary || catalog.Renderer.TelemetrySchema != telemetrySchema {
		t.Fatalf("unexpected renderer: %+v", catalog.Renderer)
	}
	if len(catalog.MapAnimations) != 1 || catalog.MapAnimations[0] != "route_line" {
		t.Fatalf("unexpected map animations: %v", catalog.MapAnimations)
	}
}

func TestLoadRejectsInvalidCatalogs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown field",
			raw:  strings.Replace(validCatalog, `"basemaps"`, `"unknown": true, "basemaps"`, 1),
			want: `unknown field "unknown"`,
		},
		{
			name: "trailing json",
			raw:  validCatalog + `{}`,
			want: "trailing JSON",
		},
		{
			name: "wrong schema",
			raw:  strings.Replace(validCatalog, schema, "chrononplugin.maps-renderer-catalog.v2", 1),
			want: "schema",
		},
		{
			name: "wrong version",
			raw:  strings.Replace(validCatalog, `"version": 1`, `"version": 2`, 1),
			want: "version 2, want 1",
		},
		{
			name: "wrong source",
			raw:  strings.Replace(validCatalog, source, "PipelineGen", 1),
			want: "source",
		},
		{
			name: "wrong renderer binary",
			raw:  strings.Replace(validCatalog, rendererBinary, "other_renderer", 1),
			want: "renderer.binary",
		},
		{
			name: "wrong telemetry schema",
			raw:  strings.Replace(validCatalog, telemetrySchema, "chronon.other.v1", 1),
			want: "renderer.telemetry_schema",
		},
		{
			name: "non-empty base flyover",
			raw:  strings.Replace(validCatalog, `"base_flyover": ""`, `"base_flyover": "territory_glow_reveal"`, 1),
			want: "base_flyover",
		},
		{
			name: "empty list",
			raw:  strings.Replace(validCatalog, `["satellite"]`, `[]`, 1),
			want: "basemaps must not be empty",
		},
		{
			name: "blank id",
			raw:  strings.Replace(validCatalog, `["route_line"]`, `[" route_line"]`, 1),
			want: "blank or untrimmed",
		},
		{
			name: "duplicate id",
			raw:  strings.Replace(validCatalog, `["route_line"]`, `["route_line", "route_line"]`, 1),
			want: "duplicate id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load([]byte(test.raw))
			if err == nil {
				t.Fatalf("Load() accepted invalid catalog")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %q, want substring %q", err, test.want)
			}
		})
	}
}

func TestSetProjectsMembership(t *testing.T) {
	set := Set([]string{"route_line", "multi_leg_flight", ""})
	if !set["route_line"] || !set["multi_leg_flight"] {
		t.Fatalf("Set() dropped members: %v", set)
	}
	if len(set) != 3 {
		t.Fatalf("Set() size = %d, want 3", len(set))
	}
	if missing := Set(nil)["route_line"]; missing {
		t.Fatalf("Set(nil) admitted an absent member")
	}
}

func TestEmbeddedCatalogLoads(t *testing.T) {
	catalog := Embedded()
	if len(catalog.MapAnimations) == 0 || len(catalog.CameraAnimations) == 0 ||
		len(catalog.LabelAnimations) == 0 || len(catalog.Basemaps) == 0 {
		t.Fatalf("embedded catalog has an empty vocabulary: %+v", catalog)
	}
	if catalog.Schema != schema || catalog.Version != version {
		t.Fatalf("embedded catalog identity = %q/%d", catalog.Schema, catalog.Version)
	}
}
