// Package geofeatures loads locally supplied Natural Earth GeoJSON into a
// deterministic, provider-neutral feature registry. It performs no network
// access and rejects malformed geometry, duplicate IDs, unresolved hierarchy,
// and ambiguous name lookups instead of guessing.
package geofeatures

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const WorldID = "world"

var (
	defaultRegistryOnce sync.Once
	defaultRegistry     *Registry
	defaultRegistryErr  error
)

// Level is the administrative level represented by a dataset.
type Level string

const (
	Country Level = "country"
	Admin1  Level = "admin1"
)

var (
	ErrNotFound  = errors.New("geofeatures: feature not found")
	ErrAmbiguous = errors.New("geofeatures: ambiguous feature name")
)

// Bounds are geographic extrema in WGS84 degrees.
type Bounds struct {
	West  float64 `json:"west"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	North float64 `json:"north"`
}

// Feature is the canonical compact view of a Natural Earth feature. Hierarchy
// contains IDs from world through this feature, inclusive.
type Feature struct {
	ID        string     `json:"id"`
	Type      Level      `json:"type"`
	Name      string     `json:"name"`
	Hierarchy []string   `json:"hierarchy"`
	Bounds    Bounds     `json:"bounds"`
	Centroid  [2]float64 `json:"centroid"` // [longitude, latitude]
}

// Registry indexes the country and admin-1 features loaded from supplied
// Natural Earth datasets. The source datasets remain owned by ChrononTemplate;
// callers decide how to provide them (filesystem, embed.FS, or another local FS).
type Registry struct {
	byID     map[string]Feature
	byName   map[string][]string
	aliases  map[string][]string
	children map[string][]string
}

// LoadNaturalEarth reads the Admin-0 country collection and zero or more
// Admin-1 collections. Every Admin-1 feature must name an already loaded
// country in its "admin" property. Country data is required; no HTTP fallback
// or inferred feature is provided.
func LoadNaturalEarth(countries io.Reader, admin1 ...io.Reader) (*Registry, error) {
	if countries == nil {
		return nil, fmt.Errorf("geofeatures: country GeoJSON reader is required")
	}
	registry := &Registry{
		byID: make(map[string]Feature), byName: make(map[string][]string),
		aliases: make(map[string][]string), children: make(map[string][]string),
	}
	world := parsedFeature{Feature: Feature{
		ID: WorldID, Type: "world", Name: "World", Bounds: Bounds{West: -180, South: -90, East: 180, North: 90}, Centroid: [2]float64{0, 0},
	}, aliases: []string{"world", "earth"}}
	if err := registry.add(world, nil); err != nil {
		return nil, err
	}
	features, err := parseCollection(countries, Country)
	if err != nil {
		return nil, fmt.Errorf("geofeatures: load countries: %w", err)
	}
	worldFeature, _ := registry.ByID(WorldID)
	for _, feature := range features {
		if err := registry.add(feature, &worldFeature); err != nil {
			return nil, err
		}
	}
	for datasetIndex, reader := range admin1 {
		if reader == nil {
			return nil, fmt.Errorf("geofeatures: admin-1 dataset %d is nil", datasetIndex)
		}
		features, err := parseCollection(reader, Admin1)
		if err != nil {
			return nil, fmt.Errorf("geofeatures: load admin-1 dataset %d: %w", datasetIndex, err)
		}
		for _, feature := range features {
			parent, err := registry.ByName(feature.parentName)
			if err != nil || parent.Type != Country {
				if err == nil {
					err = fmt.Errorf("resolved parent %q is not a country", parent.ID)
				}
				return nil, fmt.Errorf("geofeatures: admin-1 feature %q parent %q: %w", feature.Name, feature.parentName, err)
			}
			if err := registry.add(feature, &parent); err != nil {
				return nil, err
			}
		}
	}
	return registry, nil
}

// DefaultRegistry loads and caches the Natural Earth catalogs owned by
// ChrononTemplate. CHRONONTEMPLATE_CATALOG may point directly at that catalog
// directory; otherwise the loader searches the current directory and its
// parents for ChrononTemplate/catalog. Missing data is reported, never fetched.
func DefaultRegistry() (*Registry, error) {
	defaultRegistryOnce.Do(func() {
		catalogDir := strings.TrimSpace(os.Getenv("CHRONONTEMPLATE_CATALOG"))
		if catalogDir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				defaultRegistryErr = fmt.Errorf("geofeatures: locate catalog: %w", err)
				return
			}
			for dir := cwd; ; dir = filepath.Dir(dir) {
				candidate := filepath.Join(dir, "ChrononTemplate", "catalog")
				if info, err := os.Stat(filepath.Join(candidate, "ne_50m_admin_0_countries.geojson")); err == nil && !info.IsDir() {
					catalogDir = candidate
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					defaultRegistryErr = fmt.Errorf("geofeatures: Natural Earth catalog not found; set CHRONONTEMPLATE_CATALOG")
					return
				}
			}
		}
		countryFile, err := os.Open(filepath.Join(catalogDir, "ne_50m_admin_0_countries.geojson"))
		if err != nil {
			defaultRegistryErr = fmt.Errorf("geofeatures: open Natural Earth country catalog: %w", err)
			return
		}
		defer countryFile.Close()
		admin1File, err := os.Open(filepath.Join(catalogDir, "ne_10m_admin_1_gujarat.geojson"))
		if err != nil {
			defaultRegistryErr = fmt.Errorf("geofeatures: open Natural Earth admin-1 catalog: %w", err)
			return
		}
		defer admin1File.Close()
		defaultRegistry, defaultRegistryErr = LoadNaturalEarth(countryFile, admin1File)
	})
	return defaultRegistry, defaultRegistryErr
}

// ByID returns a copy of the feature with the exact canonical ID.
func (r *Registry) ByID(id string) (Feature, error) {
	if r == nil {
		return Feature{}, fmt.Errorf("geofeatures: nil registry")
	}
	feature, ok := r.byID[id]
	if !ok {
		return Feature{}, fmt.Errorf("%w: id %q", ErrNotFound, id)
	}
	return clone(feature), nil
}

// ByName resolves a canonical name or Natural Earth alias. Ambiguous aliases
// fail closed; use ByNameUnder when a parent context is available.
func (r *Registry) ByName(name string) (Feature, error) {
	if r == nil {
		return Feature{}, fmt.Errorf("geofeatures: nil registry")
	}
	ids := r.byName[normalize(name)]
	if len(ids) == 0 {
		return Feature{}, fmt.Errorf("%w: name %q", ErrNotFound, name)
	}
	if len(ids) != 1 {
		return Feature{}, fmt.Errorf("%w: name %q resolves to %v", ErrAmbiguous, name, ids)
	}
	return clone(r.byID[ids[0]]), nil
}

// ByNameUnder resolves a feature name only among children of the named parent.
func (r *Registry) ByNameUnder(name, parentID string) (Feature, error) {
	if r == nil {
		return Feature{}, fmt.Errorf("geofeatures: nil registry")
	}
	parent, ok := r.byID[parentID]
	if !ok {
		return Feature{}, fmt.Errorf("%w: parent id %q", ErrNotFound, parentID)
	}
	var match *Feature
	for _, childID := range r.children[parent.ID] {
		candidate := r.byID[childID]
		if contains(r.aliases[childID], normalize(name)) {
			if match != nil {
				return Feature{}, fmt.Errorf("%w: %q under %q", ErrAmbiguous, name, parentID)
			}
			copy := candidate
			match = &copy
		}
	}
	if match == nil {
		return Feature{}, fmt.Errorf("%w: name %q under %q", ErrNotFound, name, parentID)
	}
	return clone(*match), nil
}

// Children returns immediate children in canonical ID order.
func (r *Registry) Children(parentID string) ([]Feature, error) {
	if r == nil {
		return nil, fmt.Errorf("geofeatures: nil registry")
	}
	if _, ok := r.byID[parentID]; !ok {
		return nil, fmt.Errorf("%w: parent id %q", ErrNotFound, parentID)
	}
	ids := r.children[parentID]
	out := make([]Feature, 0, len(ids))
	for _, id := range ids {
		out = append(out, clone(r.byID[id]))
	}
	return out, nil
}

// Features returns every loaded feature in canonical ID order.
func (r *Registry) Features() []Feature {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Feature, 0, len(ids))
	for _, id := range ids {
		out = append(out, clone(r.byID[id]))
	}
	return out
}

func (r *Registry) add(parsed parsedFeature, parent *Feature) error {
	feature := parsed.Feature
	if _, exists := r.byID[feature.ID]; exists {
		return fmt.Errorf("geofeatures: duplicate feature id %q", feature.ID)
	}
	if feature.ID == WorldID {
		feature.Hierarchy = []string{WorldID}
	} else if parent == nil {
		feature.Hierarchy = []string{WorldID, feature.ID}
	} else {
		feature.Hierarchy = append(append([]string(nil), parent.Hierarchy...), feature.ID)
		r.children[parent.ID] = append(r.children[parent.ID], feature.ID)
	}
	r.byID[feature.ID] = clone(feature)
	for _, alias := range parsed.aliases {
		key := normalize(alias)
		if key == "" {
			continue
		}
		if !contains(r.aliases[feature.ID], key) {
			r.aliases[feature.ID] = append(r.aliases[feature.ID], key)
		}
		ids := r.byName[key]
		if !contains(ids, feature.ID) {
			r.byName[key] = append(ids, feature.ID)
			sort.Strings(r.byName[key])
		}
	}
	return nil
}

// parsedFeature keeps aliases and the Admin-1 parent name out of the public
// contract while the collection is assembled.
type parsedFeature struct {
	Feature
	aliases    []string
	parentName string
}

type featureCollection struct {
	Type     string            `json:"type"`
	Name     string            `json:"name,omitempty"`
	BBox     json.RawMessage   `json:"bbox,omitempty"`
	CRS      json.RawMessage   `json:"crs,omitempty"`
	Features []json.RawMessage `json:"features"`
}

type rawFeature struct {
	Type       string                     `json:"type"`
	ID         json.RawMessage            `json:"id,omitempty"`
	BBox       json.RawMessage            `json:"bbox,omitempty"`
	Properties map[string]json.RawMessage `json:"properties"`
	Geometry   json.RawMessage            `json:"geometry"`
}

type rawGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

func parseCollection(reader io.Reader, level Level) ([]parsedFeature, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var collection featureCollection
	if err := decoder.Decode(&collection); err != nil {
		return nil, fmt.Errorf("decode FeatureCollection: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	if collection.Type != "FeatureCollection" || len(collection.Features) == 0 {
		return nil, fmt.Errorf("expected non-empty FeatureCollection")
	}
	out := make([]parsedFeature, 0, len(collection.Features))
	for index, raw := range collection.Features {
		var feature rawFeature
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&feature); err != nil {
			return nil, fmt.Errorf("feature[%d]: %w", index, err)
		}
		if feature.Type != "Feature" || len(feature.Properties) == 0 || len(feature.Geometry) == 0 {
			return nil, fmt.Errorf("feature[%d]: type, properties, and geometry are required", index)
		}
		nameKey := "ADMIN"
		if level == Admin1 {
			nameKey = "name"
		}
		name, err := propertyString(feature.Properties, nameKey)
		if err != nil {
			return nil, fmt.Errorf("feature[%d].properties.%s: %w", index, nameKey, err)
		}
		id := ""
		parentName := ""
		aliases := []string{name}
		if level == Country {
			iso, _ := optionalPropertyString(feature.Properties, "ISO_A3")
			if !validISO3(iso) {
				// Natural Earth uses -99 for several disputed territories and
				// dependent areas. ADM0_A3 is the dataset's stable feature code
				// for those records (for example NOR/FRA/SOL); never discard a
				// real feature or invent a code from its display name.
				iso, _ = optionalPropertyString(feature.Properties, "ADM0_A3")
			}
			if !validISO3(iso) {
				return nil, fmt.Errorf("feature[%d].properties.ISO_A3/ADM0_A3 must provide a three-letter code", index)
			}
			id = "country:" + iso
			for _, key := range []string{"NAME", "NAME_EN", "NAME_LONG", "ADMIN", "ISO_A3", "ADM0_A3"} {
				if value, ok := optionalPropertyString(feature.Properties, key); ok {
					aliases = append(aliases, value)
				}
			}
			if value, ok := optionalPropertyString(feature.Properties, "NAME_ALT"); ok {
				for _, alias := range strings.Split(value, "|") {
					aliases = append(aliases, alias)
				}
			}
		} else {
			parentName, err = propertyString(feature.Properties, "admin")
			if err != nil {
				return nil, fmt.Errorf("feature[%d].properties.admin: %w", index, err)
			}
			parent := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(parentName), " ", "-"))
			child := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
			id = "admin1:" + parent + ":" + child
		}
		bounds, centroid, err := geometryBoundsAndCentroid(feature.Geometry)
		if err != nil {
			return nil, fmt.Errorf("feature[%d] %q geometry: %w", index, name, err)
		}
		out = append(out, parsedFeature{
			Feature: Feature{ID: id, Type: level, Name: strings.TrimSpace(name), Bounds: bounds, Centroid: centroid},
			aliases: aliases, parentName: parentName,
		})
	}
	return out, nil
}

func geometryBoundsAndCentroid(raw json.RawMessage) (Bounds, [2]float64, error) {
	var geometry rawGeometry
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&geometry); err != nil {
		return Bounds{}, [2]float64{}, fmt.Errorf("decode geometry: %w", err)
	}
	var polygons [][][][2]float64
	switch geometry.Type {
	case "Polygon":
		var coordinates [][][2]float64
		if err := json.Unmarshal(geometry.Coordinates, &coordinates); err != nil {
			return Bounds{}, [2]float64{}, fmt.Errorf("decode Polygon coordinates: %w", err)
		}
		polygons = append(polygons, coordinates)
	case "MultiPolygon":
		if err := json.Unmarshal(geometry.Coordinates, &polygons); err != nil {
			return Bounds{}, [2]float64{}, fmt.Errorf("decode MultiPolygon coordinates: %w", err)
		}
	default:
		return Bounds{}, [2]float64{}, fmt.Errorf("unsupported geometry type %q (Polygon and MultiPolygon required)", geometry.Type)
	}
	if len(polygons) == 0 {
		return Bounds{}, [2]float64{}, fmt.Errorf("polygon geometry has no rings")
	}
	bounds := Bounds{West: math.Inf(1), South: math.Inf(1), East: math.Inf(-1), North: math.Inf(-1)}
	var weightedArea, weightedLat, weightedLonX, weightedLonY float64
	for polygonIndex, polygon := range polygons {
		if len(polygon) == 0 {
			return Bounds{}, [2]float64{}, fmt.Errorf("polygon[%d] has no outer ring", polygonIndex)
		}
		var signedArea, cxNumerator, cyNumerator float64
		for ringIndex, ring := range polygon {
			if len(ring) < 4 || ring[0] != ring[len(ring)-1] {
				return Bounds{}, [2]float64{}, fmt.Errorf("polygon[%d].ring[%d] must be closed and contain at least four positions", polygonIndex, ringIndex)
			}
			xs := make([]float64, len(ring))
			for i, point := range ring {
				if !finite(point[0]) || point[0] < -180 || point[0] > 180 || !finite(point[1]) || point[1] < -90 || point[1] > 90 {
					return Bounds{}, [2]float64{}, fmt.Errorf("polygon[%d].ring[%d].position[%d] is outside finite WGS84 bounds", polygonIndex, ringIndex, i)
				}
				bounds.West = math.Min(bounds.West, point[0])
				bounds.East = math.Max(bounds.East, point[0])
				bounds.South = math.Min(bounds.South, point[1])
				bounds.North = math.Max(bounds.North, point[1])
				xs[i] = point[0]
				if i > 0 {
					for xs[i]-xs[i-1] > 180 {
						xs[i] -= 360
					}
					for xs[i]-xs[i-1] < -180 {
						xs[i] += 360
					}
				}
			}
			area2, cx6, cy6 := ringCentroidSums(xs, ring)
			if math.Abs(area2) < 1e-12 {
				return Bounds{}, [2]float64{}, fmt.Errorf("polygon[%d].ring[%d] has zero area", polygonIndex, ringIndex)
			}
			signedArea += area2 / 2
			cxNumerator += cx6 / 6
			cyNumerator += cy6 / 6
		}
		if math.Abs(signedArea) < 1e-12 {
			return Bounds{}, [2]float64{}, fmt.Errorf("polygon[%d] has zero net area", polygonIndex)
		}
		area := math.Abs(signedArea)
		cx, cy := cxNumerator/signedArea, cyNumerator/signedArea
		angle := cx * math.Pi / 180
		weightedArea += area
		weightedLonX += math.Cos(angle) * area
		weightedLonY += math.Sin(angle) * area
		weightedLat += cy * area
	}
	if weightedArea == 0 {
		return Bounds{}, [2]float64{}, fmt.Errorf("geometry has zero area")
	}
	centroidLon := math.Atan2(weightedLonY, weightedLonX) * 180 / math.Pi
	centroidLat := weightedLat / weightedArea
	if !finite(centroidLon) || !finite(centroidLat) || centroidLat < -90 || centroidLat > 90 {
		return Bounds{}, [2]float64{}, fmt.Errorf("computed centroid is invalid")
	}
	return bounds, [2]float64{centroidLon, centroidLat}, nil
}

func ringCentroidSums(xs []float64, ring [][2]float64) (area2, cx6, cy6 float64) {
	for i := 0; i < len(ring)-1; i++ {
		x1, y1 := xs[i], ring[i][1]
		x2, y2 := xs[i+1], ring[i+1][1]
		cross := x1*y2 - x2*y1
		area2 += cross
		cx6 += (x1 + x2) * cross
		cy6 += (y1 + y2) * cross
	}
	return area2, cx6, cy6
}

func propertyString(properties map[string]json.RawMessage, key string) (string, error) {
	value, ok := optionalPropertyString(properties, key)
	if !ok {
		return "", fmt.Errorf("required non-empty string is missing")
	}
	return value, nil
}

func optionalPropertyString(properties map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := properties[key]
	if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func normalize(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func clone(feature Feature) Feature {
	feature.Hierarchy = append([]string(nil), feature.Hierarchy...)
	return feature
}

func validISO3(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
