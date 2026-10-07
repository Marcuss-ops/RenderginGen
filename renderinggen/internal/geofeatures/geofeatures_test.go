package geofeatures

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func naturalEarthPath(t *testing.T, name string) string {
	t.Helper()
	for dir := ".."; ; dir = filepath.Join(dir, "..") {
		path := filepath.Join(dir, "ChrononTemplate", "catalog", name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate ChrononTemplate/catalog/%s from working directory", name)
		}
	}
}

func openNaturalEarth(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.Open(naturalEarthPath(t, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestDefaultRegistryLoadsCanonicalLocalData(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ByName("United Kingdom"); err != nil {
		t.Fatalf("default registry lacks canonical country: %v", err)
	}
}

func TestLoadNaturalEarthRealCountryAndAdmin1Datasets(t *testing.T) {
	registry, err := LoadNaturalEarth(
		openNaturalEarth(t, "ne_50m_admin_0_countries.geojson"),
		openNaturalEarth(t, "ne_10m_admin_1_gujarat.geojson"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(registry.Features()); got != 244 {
		t.Fatalf("loaded feature count = %d, want world, 242 countries, and Gujarat", got)
	}
	uk, err := registry.ByName("United Kingdom")
	if err != nil {
		t.Fatal(err)
	}
	if uk.ID != "country:GBR" || uk.Type != Country || len(uk.Hierarchy) != 2 || uk.Hierarchy[0] != WorldID {
		t.Fatalf("unexpected country record: %+v", uk)
	}
	if uk.Bounds.West >= uk.Bounds.East || uk.Bounds.South >= uk.Bounds.North || !finite(uk.Centroid[0]) || !finite(uk.Centroid[1]) {
		t.Fatalf("country geometry summary is invalid: %+v", uk)
	}
	india, err := registry.ByName("India")
	if err != nil {
		t.Fatal(err)
	}
	gujarat, err := registry.ByNameUnder("Gujarat", india.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gujarat.Type != Admin1 || len(gujarat.Hierarchy) != 3 || gujarat.Hierarchy[0] != WorldID || gujarat.Hierarchy[1] != india.ID || gujarat.Bounds.West < 68 || gujarat.Bounds.East > 75 {
		t.Fatalf("unexpected admin-1 record: %+v", gujarat)
	}
	children, err := registry.Children(india.ID)
	if err != nil || len(children) != 1 || children[0].ID != gujarat.ID {
		t.Fatalf("India children = %+v, err %v; want Gujarat only in pinned dataset", children, err)
	}
}

func TestLookupFailsClosedForMissingAndAmbiguousNames(t *testing.T) {
	registry, err := LoadNaturalEarth(openNaturalEarth(t, "ne_50m_admin_0_countries.geojson"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ByName("Definitely not a country"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown name error = %v, want ErrNotFound", err)
	}
	if _, err := registry.ByName("Democratic Republic of the Congo"); err != nil {
		t.Fatalf("canonical country name should resolve, got %v", err)
	}
	// The shorter Natural Earth alias is ambiguous only when both the
	// Republic of the Congo name and the DRC alias are indexed. The canonical
	// dataset stores "Congo" as the Republic's NAME rather than NAME_ALT;
	// this must therefore resolve exactly, not be incorrectly rejected.
	if feature, err := registry.ByName("Congo"); err != nil || feature.ID != "country:COG" {
		t.Fatalf("canonical Congo name = %+v, err %v", feature, err)
	}
	if _, err := registry.ByNameUnder("Gujarat", "country:USA"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing scoped feature error = %v, want ErrNotFound", err)
	}
}

func TestCountryBoundsCentroidAndFeatureCopiesAreStable(t *testing.T) {
	registry, err := LoadNaturalEarth(openNaturalEarth(t, "ne_50m_admin_0_countries.geojson"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.ByID("country:GBR")
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.ByID("country:GBR")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated lookup changed result: %+v vs %+v", first, second)
	}
	first.Hierarchy[0] = "mutated"
	again, err := registry.ByID("country:GBR")
	if err != nil || again.Hierarchy[0] != WorldID {
		t.Fatalf("caller mutation escaped registry copy: %+v, err %v", again, err)
	}
	if math.Abs(again.Centroid[1]) > 90 || again.Bounds.West >= again.Bounds.East {
		t.Fatalf("invalid bounds or centroid: %+v", again)
	}
}

func TestLoadNaturalEarthRejectsMalformedOrUnresolvedData(t *testing.T) {
	countries := `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"ADMIN":"A","ISO_A3":"AAA"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}}]}`
	for name, dataset := range map[string]string{
		"broken geometry":     strings.Replace(countries, `[[0,0],[1,0],[1,1],[0,1],[0,0]]`, `[[0,0],[1,0],[1,1]]`, 1),
		"unknown geometry":    strings.Replace(countries, `"type":"Polygon"`, `"type":"Point"`, 1),
		"missing ISO":         strings.Replace(countries, `,"ISO_A3":"AAA"`, "", 1),
		"extra feature field": strings.Replace(countries, `"type":"Feature"`, `"type":"Feature","surprise":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadNaturalEarth(strings.NewReader(dataset)); err == nil {
				t.Fatal("malformed country data must fail closed")
			}
		})
	}
	admin1 := `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"name":"Area","admin":"Unknown"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}}]}`
	if _, err := LoadNaturalEarth(strings.NewReader(countries), strings.NewReader(admin1)); err == nil || !strings.Contains(err.Error(), "parent") {
		t.Fatalf("admin-1 feature with no loaded parent must fail, got %v", err)
	}
}

func TestLoadNaturalEarthRequiresExactlyOneJSONCollection(t *testing.T) {
	good := `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"ADMIN":"A","ISO_A3":"AAA"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}}]}`
	if _, err := LoadNaturalEarth(bytes.NewBufferString(good + ` {}`)); err == nil {
		t.Fatal("trailing JSON values must be rejected")
	}
}
