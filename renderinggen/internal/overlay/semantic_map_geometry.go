// semantic_map_geometry.go owns geospatial-to-canvas label placement and its
// text-box measurements. It deliberately does not compile typography: map
// layout resolves position and box constraints; text roles own appearance.
package overlay

import (
	"fmt"
	"math"
	"sort"
	"unicode/utf8"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
)

// resolveMapLabelPlacements computes deterministic screen-space placements for
// all pins before compilation, so placement errors remain contract errors.
func resolveMapLabelPlacements(pins []SemanticMapPin, window geo.Window, canvasW, canvasH int) (map[string][2]float64, error) {
	placements := make(map[string][2]float64, len(pins))
	order := make([]int, 0, len(pins))
	for index := range pins {
		order = append(order, index)
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := pins[order[i]], pins[order[j]]
		if left.LabelPriority != right.LabelPriority {
			return left.LabelPriority > right.LabelPriority
		}
		return left.ID < right.ID
	})
	placed := make([][4]float64, 0, len(order))
	for _, index := range order {
		pin := pins[index]
		x, y := window.ToRaster(pin.Latitude, pin.Longitude)
		x *= float64(canvasW) / float64(window.Width)
		y *= float64(canvasH) / float64(window.Height)
		width, height := mapPinLabelDimensions(pin, canvasW, canvasH)
		candidates := mapPinLabelCandidates(pin, x, y, width, height)
		found := false
		for _, candidate := range candidates {
			left := clampMapBox(candidate[0], float64(canvasW)-width)
			top := clampMapBox(candidate[1], float64(canvasH)-height)
			if math.Abs(left-candidate[0]) >= 0.001 || math.Abs(top-candidate[1]) >= 0.001 {
				continue
			}
			rect := [4]float64{left, top, left + width, top + height}
			collision := false
			for _, previous := range placed {
				if mapLabelRectsOverlap(rect, previous) {
					collision = true
					break
				}
			}
			if !collision {
				placed = append(placed, rect)
				placements[pin.ID] = [2]float64{left, top}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("pin %q label cannot be placed without clipping or colliding; move it or adjust label_offset_px", pin.ID)
		}
	}
	return placements, nil
}

func mapTypographyScale(canvasW, canvasH int) float64 {
	scale := math.Sqrt(float64(canvasW*canvasH) / float64(1280*720))
	return math.Max(0.65, math.Min(2.2, scale))
}

// mapPinLabelDimensions derives the map-owned box from label content, canvas
// scale, and optional background padding; it does not choose text appearance.
func mapPinLabelDimensions(pin SemanticMapPin, canvasW, canvasH int) (float64, float64) {
	fontSize := mapPinLabelFontPX
	if pin.LabelStyle != nil && pin.LabelStyle.FontSizePX != nil {
		fontSize = *pin.LabelStyle.FontSizePX
	}
	scale := mapTypographyScale(canvasW, canvasH)
	width := math.Max(96*scale, float64(utf8.RuneCountInString(pin.Label))*fontSize*scale*0.68+24*scale)
	height := math.Max(mapPinLabelHeightPX*scale, fontSize*scale*1.6)
	if plate := pin.LabelStyle; plate != nil && plate.Background != nil && len(plate.Background.Padding) == 2 {
		width += 2 * plate.Background.Padding[0] * scale
		height += 2 * plate.Background.Padding[1] * scale
	}
	return math.Min(width, float64(canvasW)), math.Min(height, float64(canvasH))
}

// mapPinLabelCandidates is the geometry resolver's stable candidate order:
// below, above, right, left, then outward rings around the grounded pin.
func mapPinLabelCandidates(pin SemanticMapPin, x, y, width, height float64) [][2]float64 {
	offsetX, offsetY := 0.0, 0.0
	if len(pin.LabelOffsetPX) == 2 {
		offsetX, offsetY = pin.LabelOffsetPX[0], pin.LabelOffsetPX[1]
	}
	candidates := [][2]float64{
		{x - width/2 + offsetX, y + pin.RadiusPX + mapPinLabelGapPX + offsetY},
		{x - width/2, y - pin.RadiusPX - mapPinLabelGapPX - height},
		{x + pin.RadiusPX + mapPinLabelGapPX, y - height/2},
		{x - pin.RadiusPX - mapPinLabelGapPX - width, y - height/2},
	}
	for ring := 1; ring <= 8; ring++ {
		distance := float64(ring) * (height + mapLabelCollisionGap)
		for _, direction := range [][2]float64{{0, -1}, {1, 0}, {0, 1}, {-1, 0}, {1, -1}, {1, 1}, {-1, 1}, {-1, -1}} {
			candidates = append(candidates, [2]float64{
				x - width/2 + direction[0]*distance,
				y - height/2 + direction[1]*distance,
			})
		}
	}
	return candidates
}

func mapLabelRectsOverlap(a, b [4]float64) bool {
	return a[0] < b[2]+mapLabelCollisionGap && a[2]+mapLabelCollisionGap > b[0] &&
		a[1] < b[3]+mapLabelCollisionGap && a[3]+mapLabelCollisionGap > b[1]
}
