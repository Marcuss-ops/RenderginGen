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

// fixturePath is the package's OWN pinned catalog. It is what makes this
// package's unit tests hermetic: the loader's contract (hierarchy, bounds,
// centroid, lookup, ambiguity, copies, fail-closed parsing) is covered against
// data that ships with the test, so `go test ./internal/geofeatures` passes on
// a bare checkout with no sibling ChrononTemplate clone.
func fixturePath(name string) string { return filepath.Join("testdata", "catalog", name) }

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.Open(fixturePath(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func fixtureRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := LoadNaturalEarth(
		openFixture(t, "ne_50m_admin_0_countries.geojson"),
		openFixture(t, "ne_10m_admin_1_gujarat.geojson"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// findNaturalEarth searches for a ChrononTemplate-owned Natural Earth catalog
// from the working directory upward. It REPORTS absence instead of failing, so
// the caller decides what absence means: a skip in the unit lane, an assertion
// in the integration lane. Returning an error/fatal here is what made the whole
// package red on a clone without the sibling repository.
// The walk starts from an ABSOLUTE working directory on purpose. A relative
// start ("..") never terminates: filepath.Dir("../..") is "..", and joining
// another ".." only lengthens the path, so the parent==dir stopping condition
// is unreachable and a missing catalog becomes an infinite stat loop that
// eventually dies on ENAMETOOLONG. Absolute paths do reach "/", where
// filepath.Dir("/") == "/" stops the walk.
func findNaturalEarth(name string) (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		path := filepath.Join(dir, "ChrononTemplate", "catalog", name)
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			return path, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// naturalEarthPath locates the ChrononTemplate-owned full Natural Earth
// catalog. That checkout belongs to the INTEGRATION lane: a unit job must not
// depend on another repository being present, so an absent catalog SKIPS these
// full-dataset assertions instead of failing them. The same contract is still
// covered hermetically by the pinned fixtures above.
func naturalEarthPath(t *testing.T, name string) string {
	t.Helper()
	path, ok := findNaturalEarth(name)
	if !ok {
		t.Skipf("ChrononTemplate/catalog/%s not present (sibling checkout); skipping the full-dataset integration assertions", name)
	}
	return path
}

// TestAbsentNaturalEarthCatalogIsReportedNotFatal pins the mechanism that makes
// the integration tests skippable: absence is a reported fact. If this ever goes
// back to a fatal, the unit lane fails on every checkout that does not have
// ChrononTemplate cloned next to this repository.
func TestAbsentNaturalEarthCatalogIsReportedNotFatal(t *testing.T) {
	if _, ok := findNaturalEarth("definitely-not-a-natural-earth-catalog.geojson"); ok {
		t.Fatal("lookup reported a catalog file that does not exist")
	}
	// And the real name resolves here only because this checkout has the sibling
	// repository; either answer is acceptable, the point is that it is an answer.
	if path, ok := findNaturalEarth("ne_50m_admin_0_countries.geojson"); ok && path == "" {
		t.Fatal("lookup reported success with an empty path")
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

// TestDefaultRegistryLoadsFromTheConfiguredCatalog drives the loader through
// CHRONONTEMPLATE_CATALOG — the documented override a deployment uses to ship
// the catalog separately — pointed at this package's own fixture. Before this
// the test loaded whatever sibling checkout happened to be on the machine, so
// the unit job failed on a clean clone and passed for reasons that had nothing
// to do with the code under test.
func TestDefaultRegistryLoadsFromTheConfiguredCatalog(t *testing.T) {
	t.Setenv("CHRONONTEMPLATE_CATALOG", filepath.Join("testdata", "catalog"))
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ByName("United Kingdom"); err != nil {
		t.Fatalf("default registry lacks the fixture country: %v", err)
	}
}

// TestLoadNaturalEarthFromPinnedFixtures carries the semantics the full-dataset
// assertions used to carry, but against data that ships with the test: the
// hierarchy, the geometry summary, the scoped lookup and the child index are
// all exercised without a sibling checkout.
func TestLoadNaturalEarthFromPinnedFixtures(t *testing.T) {
	registry := fixtureRegistry(t)

	// world + four countries + one admin-1 region.
	if got := len(registry.Features()); got != 6 {
		t.Fatalf("loaded feature count = %d, want 6", got)
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
	// The NAME_ALT alias and the long name must both resolve to the same record.
	for _, alias := range []string{"Britain", "United Kingdom of Great Britain and Northern Ireland"} {
		if feature, err := registry.ByName(alias); err != nil || feature.ID != uk.ID {
			t.Errorf("alias %q = %+v, err %v; want %s", alias, feature, err, uk.ID)
		}
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
		t.Fatalf("India children = %+v, err %v; want Gujarat only", children, err)
	}
	if _, err := registry.ByNameUnder("Gujarat", "country:USA"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent error = %v, want ErrNotFound", err)
	}
}

// TestLookupFailsClosedForAmbiguousAliases pins the conservative half of name
// resolution: when two features share an alias, resolution refuses instead of
// picking one. Guessing would render the wrong country's geometry on a map,
// which is a visual error no later stage can detect.
func TestLookupFailsClosedForAmbiguousAliases(t *testing.T) {
	registry := fixtureRegistry(t)

	if _, err := registry.ByName("Congo"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguous alias error = %v, want ErrAmbiguous", err)
	}
	// The canonical admin names stay unambiguous.
	if feature, err := registry.ByName("Republic of the Congo"); err != nil || feature.ID != "country:COG" {
		t.Fatalf("canonical name = %+v, err %v", feature, err)
	}
	if feature, err := registry.ByName("Democratic Republic of the Congo"); err != nil || feature.ID != "country:COD" {
		t.Fatalf("canonical name = %+v, err %v", feature, err)
	}
	if _, err := registry.ByName("Definitely not a country"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown name error = %v, want ErrNotFound", err)
	}

	// A caller mutating a returned record must not reach the registry's copy.
	first, err := registry.ByID("country:GBR")
	if err != nil {
		t.Fatal(err)
	}
	first.Hierarchy[0] = "mutated"
	second, err := registry.ByID("country:GBR")
	if err != nil || second.Hierarchy[0] != WorldID {
		t.Fatalf("caller mutation escaped the registry copy: %+v, err %v", second, err)
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
