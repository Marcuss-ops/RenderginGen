package tacticalfill

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
)

func loadRegistry(t *testing.T) *geofeatures.Registry {
	t.Helper()
	countries, err := os.Open("../geofeatures/testdata/catalog/ne_50m_admin_0_countries.geojson")
	if err != nil {
		t.Fatalf("open country fixture: %v", err)
	}
	defer countries.Close()
	admin1, err := os.Open("../geofeatures/testdata/catalog/ne_10m_admin_1_gujarat.geojson")
	if err != nil {
		t.Fatalf("open admin-1 fixture: %v", err)
	}
	defer admin1.Close()
	registry, err := geofeatures.LoadNaturalEarth(countries, admin1)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return registry
}

func TestBuildFitsCountryBesideTheName(t *testing.T) {
	sample, feature, err := Build(loadRegistry(t), "United Kingdom", Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if feature.ID != "country:GBR" {
		t.Fatalf("feature = %q, want country:GBR", feature.ID)
	}
	if sample.Schema != Schema || sample.Canvas != [2]int{DefaultWidth, DefaultHeight} {
		t.Fatalf("sample header = %q %v", sample.Schema, sample.Canvas)
	}
	if sample.Title != "UNITED KINGDOM" || sample.Subtitle != "" {
		t.Fatalf("title/subtitle = %q/%q", sample.Title, sample.Subtitle)
	}
	if len(sample.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(sample.Regions))
	}
	points := sample.Regions[0].Points
	if len(points) < 4 || points[0] != points[len(points)-1] {
		t.Fatalf("ring is not a closed loop: %d points", len(points))
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, point := range points {
		if point[0] < -0.01 || point[0] > DefaultWidth+0.01 || point[1] < -0.01 || point[1] > DefaultHeight+0.01 {
			t.Fatalf("point outside canvas: %v", point)
		}
		minX, maxX = math.Min(minX, point[0]), math.Max(maxX, point[0])
		minY, maxY = math.Min(minY, point[1]), math.Max(maxY, point[1])
	}
	wantX := (DefaultTerritoryBox.MinX + DefaultTerritoryBox.MaxX) / 2 * DefaultWidth
	wantY := (DefaultTerritoryBox.MinY + DefaultTerritoryBox.MaxY) / 2 * DefaultHeight
	if gotX := (minX + maxX) / 2; math.Abs(gotX-wantX) > 0.5 {
		t.Fatalf("territory centre x = %.2f, want %.2f", gotX, wantX)
	}
	if gotY := (minY + maxY) / 2; math.Abs(gotY-wantY) > 0.5 {
		t.Fatalf("territory centre y = %.2f, want %.2f", gotY, wantY)
	}
}

func TestBuildAdmin1UsesParentAsSubtitle(t *testing.T) {
	sample, feature, err := Build(loadRegistry(t), "admin1:india:gujarat", Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if feature.Type != geofeatures.Admin1 {
		t.Fatalf("feature type = %q", feature.Type)
	}
	if sample.Title != "GUJARAT" || sample.Subtitle != "India" {
		t.Fatalf("title/subtitle = %q/%q, want GUJARAT/India", sample.Title, sample.Subtitle)
	}
}

func TestBuildKeepsEveryMultiPolygonRing(t *testing.T) {
	const collection = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"ADMIN":"Two Islands","ISO_A3":"TIS"},"geometry":{"type":"MultiPolygon","coordinates":[[[[0,0],[1,0],[1,1],[0,1],[0,0]]],[[[2,0],[3,0],[3,1],[2,1],[2,0]]]]}}]}`
	registry, err := geofeatures.LoadNaturalEarth(strings.NewReader(collection))
	if err != nil {
		t.Fatalf("load synthetic registry: %v", err)
	}
	rings, err := registry.Rings("country:TIS")
	if err != nil {
		t.Fatalf("Rings: %v", err)
	}
	if len(rings) != 2 {
		t.Fatalf("rings = %d, want 2", len(rings))
	}
	sample, _, err := Build(registry, "country:TIS", Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(sample.Regions) != 2 {
		t.Fatalf("regions = %d, want 2", len(sample.Regions))
	}
}

func TestBuildRejectsUnknownPlace(t *testing.T) {
	if _, _, err := Build(loadRegistry(t), "Atlantis", Options{}); err == nil {
		t.Fatal("Build accepted an unknown place")
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("Democratic Republic of the Congo"); got != "democratic-republic-of-the-congo" {
		t.Fatalf("Slug = %q", got)
	}
	if got := Slug("  Côte d'Ivoire  "); got != "c-te-d-ivoire" {
		t.Fatalf("Slug = %q", got)
	}
}
