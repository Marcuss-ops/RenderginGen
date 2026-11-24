// Package tacticalfill connects a Natural Earth place to ChrononTemplate's
// native tactical territory fill. RenderingGen owns the geographic decision —
// it resolves the name, reads the feature's outer rings and projects them into
// canvas space — then writes the chronontemplate.tactical-fill-sample.v1 file
// consumed by chronontemplate_emit_tactical_fill. No projection or GeoJSON
// parsing happens in ChrononTemplate.
package tacticalfill

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/mapgeom"
)

const (
	// Schema is the sample contract emitted by this package and read by
	// ChrononTemplate's tactical fill emitter.
	Schema         = "chronontemplate.tactical-fill-sample.v1"
	DefaultWidth   = 1920
	DefaultHeight  = 1080
	DefaultPadding = 0.04
	// MaxRegions and MaxRingPoints mirror TacticalFillPack's drawable limits.
	MaxRegions    = 32
	MaxRingPoints = 4096
	// DefaultEmitTimeout bounds one emitter invocation. A hung native binary
	// must not hold the command indefinitely; a caller's shorter deadline wins.
	DefaultEmitTimeout = 2 * time.Minute
)

// Box is a fraction-of-canvas rectangle in which the territory is fitted.
// The default keeps the left third free for the place name, so the title sits
// beside the polygon instead of on top of it.
type Box struct {
	MinX, MinY, MaxX, MaxY float64
}

var DefaultTerritoryBox = Box{MinX: 0.34, MinY: 0.06, MaxX: 0.96, MaxY: 0.94}

// Sample is the chronontemplate.tactical-fill-sample.v1 wire document.
type Sample struct {
	Schema   string   `json:"schema"`
	Canvas   [2]int   `json:"canvas"`
	Title    string   `json:"title,omitempty"`
	Subtitle string   `json:"subtitle,omitempty"`
	Regions  []Region `json:"regions"`
}

// Region is one closed canvas-space territory ring.
type Region struct {
	Points [][2]float64 `json:"points"`
}

// Options controls the fit. Zero values select the defaults.
type Options struct {
	Width, Height int
	Padding       float64
	Box           Box
	Title         string
	Subtitle      string
}

// Fit is the Web-Mercator framing used to place one feature in the canvas. It
// is exposed so a caller can project neighbouring geography with exactly the
// same transform instead of re-deriving the zoom and origin.
type Fit struct {
	Zoom         float64
	TopLeftX     float64
	TopLeftY     float64
	CanvasWidth  int
	CanvasHeight int
}

// Scale is the Web-Mercator world size in pixels at the fit zoom.
func (f Fit) Scale() float64 { return geo.MercatorTileSize * math.Pow(2, f.Zoom) }

// Project maps WGS84 degrees to canvas pixels (origin top-left).
func (f Fit) Project(lon, lat float64) [2]float64 {
	scale := f.Scale()
	return [2]float64{mercatorX(lon, scale) - f.TopLeftX, mercatorY(lat, scale) - f.TopLeftY}
}

// Unproject maps canvas pixels back to WGS84 degrees.
func (f Fit) Unproject(x, y float64) (lon, lat float64) {
	scale := f.Scale()
	lon = ((x+f.TopLeftX)/scale)*360 - 180
	n := math.Pi * (1 - 2*(y+f.TopLeftY)/scale)
	lat = math.Atan(math.Sinh(n)) * 180 / math.Pi
	return lon, lat
}

// Build resolves a Natural Earth feature by name or id, projects its outer
// rings into the canvas territory box and returns the sample plus the resolved
// feature (whose Name feeds the title when the caller did not supply one).
func Build(registry *geofeatures.Registry, selector string, opts Options) (Sample, geofeatures.Feature, error) {
	sample, feature, _, err := BuildWithFit(registry, selector, opts)
	return sample, feature, err
}

// BuildWithFit is Build plus the projection that produced the sample, so a
// caller can place context geography on the same plane.
func BuildWithFit(registry *geofeatures.Registry, selector string, opts Options) (Sample, geofeatures.Feature, Fit, error) {
	if registry == nil {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: feature registry is required")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: place selector is required")
	}
	width, height := opts.Width, opts.Height
	if width == 0 {
		width = DefaultWidth
	}
	if height == 0 {
		height = DefaultHeight
	}
	if width < 64 || height < 64 || width > 8192 || height > 8192 {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: canvas must be 64..8192 pixels")
	}
	padding := opts.Padding
	if padding == 0 {
		padding = DefaultPadding
	}
	if !finite(padding) || padding < 0 || padding > 0.4 {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: padding must be within [0,0.4]")
	}
	box := opts.Box
	if box == (Box{}) {
		box = DefaultTerritoryBox
	}
	if !finite(box.MinX) || !finite(box.MinY) || !finite(box.MaxX) || !finite(box.MaxY) ||
		box.MinX < 0 || box.MinY < 0 || box.MaxX > 1 || box.MaxY > 1 ||
		box.MaxX-box.MinX < 0.1 || box.MaxY-box.MinY < 0.1 {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: territory box must be a valid canvas fraction")
	}

	feature, err := registry.Resolve(selector)
	if err != nil {
		return Sample{}, geofeatures.Feature{}, Fit{}, err
	}
	rings, err := registry.Rings(feature.ID)
	if err != nil {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: %s: %w", feature.Name, err)
	}
	if len(rings) == 0 {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: %s has no polygon rings", feature.Name)
	}

	continuous, minLon, maxLon, minLat, maxLat := unwrapRings(rings, feature.Centroid[0])
	if maxLon-minLon <= 0 || maxLat-minLat <= 0 {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: %s has degenerate bounds", feature.Name)
	}

	usableWidth := float64(width) * (box.MaxX - box.MinX) * (1 - 2*padding)
	usableHeight := float64(height) * (box.MaxY - box.MinY) * (1 - 2*padding)
	spanX := (maxLon - minLon) / 360 * geo.MercatorTileSize
	spanY := math.Abs(mercatorY(maxLat, geo.MercatorTileSize) - mercatorY(minLat, geo.MercatorTileSize))
	if !finite(spanX) || !finite(spanY) || spanX <= 0 || spanY <= 0 {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: %s cannot be fitted in Web Mercator", feature.Name)
	}
	zoom := math.Min(math.Log2(usableWidth/spanX), math.Log2(usableHeight/spanY))
	if !finite(zoom) {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: %s fit zoom is invalid", feature.Name)
	}
	zoom = math.Max(0, math.Min(18, zoom))
	scale := geo.MercatorTileSize * math.Pow(2, zoom)
	// Centre the projected bounds, not the geographic midpoint: Mercator is
	// nonlinear in latitude, so the mid-latitude does not project to the middle
	// of the fitted box.
	centerX := (mercatorX(minLon, scale) + mercatorX(maxLon, scale)) / 2
	centerY := (mercatorY(minLat, scale) + mercatorY(maxLat, scale)) / 2
	boxCenterX := (box.MinX + box.MaxX) / 2 * float64(width)
	boxCenterY := (box.MinY + box.MaxY) / 2 * float64(height)
	topLeftX := centerX - boxCenterX
	topLeftY := centerY - boxCenterY
	fit := Fit{Zoom: zoom, TopLeftX: topLeftX, TopLeftY: topLeftY, CanvasWidth: width, CanvasHeight: height}

	regions := make([]Region, 0, len(continuous))
	for _, ring := range continuous {
		projected := make([][2]float64, 0, len(ring))
		for _, point := range ring {
			projectedPoint := fit.Project(point[0], point[1])
			x := projectedPoint[0]
			y := projectedPoint[1]
			projected = append(projected, [2]float64{
				math.Round(x*100) / 100,
				math.Round(y*100) / 100,
			})
		}
		projected = decimate(projected, MaxRingPoints)
		projected = closeRing(projected)
		if len(projected) < 4 || ringArea(projected) < 1 {
			continue
		}
		regions = append(regions, Region{Points: projected})
	}
	if len(regions) == 0 {
		return Sample{}, geofeatures.Feature{}, Fit{}, fmt.Errorf("tacticalfill: %s has no drawable ring after projection", feature.Name)
	}
	sortRegionsByArea(regions)
	if len(regions) > MaxRegions {
		regions = regions[:MaxRegions]
	}
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = strings.ToUpper(feature.Name)
	}
	subtitle := strings.TrimSpace(opts.Subtitle)
	if subtitle == "" && feature.Type == geofeatures.Admin1 && len(feature.Hierarchy) >= 2 {
		if parent, err := registry.ByID(feature.Hierarchy[len(feature.Hierarchy)-2]); err == nil {
			subtitle = parent.Name
		}
	}
	return Sample{
		Schema: Schema, Canvas: [2]int{width, height},
		Title: title, Subtitle: subtitle, Regions: regions,
	}, feature, fit, nil
}

// Write marshals the sample to path, creating its directory.
func Write(sample Sample, path string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("tacticalfill: create sample directory: %w", err)
		}
	}
	payload, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		return fmt.Errorf("tacticalfill: encode sample: %w", err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o644); err != nil {
		return fmt.Errorf("tacticalfill: write sample: %w", err)
	}
	return nil
}

// Emit runs ChrononTemplate's emitter to lower the sample into a render plan.
// The invocation is bounded by DefaultEmitTimeout and by ctx, whichever
// deadline is earlier; the child process is killed when either expires.
func Emit(ctx context.Context, emitter, samplePath, planPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("tacticalfill: emitter: %w", err)
	}
	if emitter == "" {
		return fmt.Errorf("tacticalfill: emitter binary not found; pass -emit or set CHRONONTEMPLATE_EMIT_TACTICAL_FILL")
	}
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		return fmt.Errorf("tacticalfill: create plan directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultEmitTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, emitter, planPath, samplePath)
	output, err := command.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("tacticalfill: emitter: %w (%v): %s", ctxErr, err, strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("tacticalfill: emitter: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ResolveEmitter resolves an explicitly configured emitter. The library does
// not discover sibling build trees: that is command-level policy, owned by
// cmd/map-tactical-fill, so an embedded caller cannot inherit a path relative
// to an unrelated working directory.
func ResolveEmitter(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv("CHRONONTEMPLATE_EMIT_TACTICAL_FILL")
}

// Slug turns a place name into a filesystem-safe stem.
func Slug(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

// unwrapRings makes each ring continuous across the antimeridian and shifts it
// near the feature centroid, returning the longitude/latitude bounds of the
// whole feature in that continuous frame.
func unwrapRings(rings [][][2]float64, centerLon float64) ([][][2]float64, float64, float64, float64, float64) {
	minLon, maxLon := math.Inf(1), math.Inf(-1)
	minLat, maxLat := math.Inf(1), math.Inf(-1)
	out := make([][][2]float64, len(rings))
	for index, ring := range rings {
		points := make([][2]float64, len(ring))
		previous := centerLon
		for i, point := range ring {
			lon := point[0]
			for lon-previous > 180 {
				lon -= 360
			}
			for lon-previous < -180 {
				lon += 360
			}
			for lon-centerLon > 180 {
				lon -= 360
			}
			for lon-centerLon < -180 {
				lon += 360
			}
			points[i] = [2]float64{lon, point[1]}
			previous = lon
			minLon = math.Min(minLon, lon)
			maxLon = math.Max(maxLon, lon)
			minLat = math.Min(minLat, point[1])
			maxLat = math.Max(maxLat, point[1])
		}
		out[index] = points
	}
	return out, minLon, maxLon, minLat, maxLat
}

// decimate applies the tactical-fill policy explicitly. It intentionally uses
// the stepped criterion, not the ModernMap1 even-budget one: the two surfaces
// have different silhouettes and must not be conflated.
func decimate(points [][2]float64, limit int) [][2]float64 {
	return mapgeom.Decimate(points, limit, mapgeom.DecimateStepped)
}

func closeRing(points [][2]float64) [][2]float64 {
	if len(points) > 0 && points[0] != points[len(points)-1] {
		return append(points, points[0])
	}
	return points
}

func ringArea(points [][2]float64) float64 {
	var twice float64
	for i := 0; i < len(points)-1; i++ {
		twice += points[i][0]*points[i+1][1] - points[i+1][0]*points[i][1]
	}
	return math.Abs(twice) / 2
}

func sortRegionsByArea(regions []Region) {
	for i := 1; i < len(regions); i++ {
		for j := i; j > 0 && ringArea(regions[j].Points) > ringArea(regions[j-1].Points); j-- {
			regions[j], regions[j-1] = regions[j-1], regions[j]
		}
	}
}

func mercatorX(lon, scale float64) float64 { return (lon + 180) / 360 * scale }

func mercatorY(lat, scale float64) float64 {
	if lat < -geo.MercatorLatitudeLimit {
		lat = -geo.MercatorLatitudeLimit
	} else if lat > geo.MercatorLatitudeLimit {
		lat = geo.MercatorLatitudeLimit
	}
	rad := lat * math.Pi / 180
	return (1 - math.Log(math.Tan(rad)+1/math.Cos(rad))/math.Pi) * 0.5 * scale
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
