// maps_renderer_catalog.go is the overlay-root compatibility layer over the
// mapcatalog leaf package. The vocabulary remains owned by ChrononPlugin; this
// file only exposes the embedded projection to existing overlay code without a
// package-wide rename.
package overlay

import "github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay/mapcatalog"

const chrononPluginMapsCatalogFileName = mapcatalog.FileName

var (
	chrononPluginMaps = mapcatalog.Embedded()

	// Map presentation vocabulary, projected from the ChrononPlugin catalog.
	// These replace the former hand-maintained lists (the retired Python
	// composition families region_zoom/pulse_marker/spotlight/ed_*), which
	// described a renderer that no longer exists.
	certifiedMapCompositions     = mapcatalog.Set(chrononPluginMaps.MapAnimations)
	certifiedMapCameraAnimations = mapcatalog.Set(chrononPluginMaps.CameraAnimations)
	certifiedMapLabelAnimations  = mapcatalog.Set(chrononPluginMaps.LabelAnimations)
)

func loadChrononPluginMapsCatalog(raw []byte) (mapcatalog.Catalog, error) {
	return mapcatalog.Load(raw)
}
