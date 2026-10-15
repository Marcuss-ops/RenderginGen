package overlay

import (
	"fmt"
	"math"
)

// AE-style lens data for the whole-scene camera.
//
// After Effects exposes camera presets as 35mm-equivalent focal lengths on a
// 36x24mm gate (filmGauge 35 in CM3D terms). The vertical field of view for a
// focal length on that gate is the closed form below; the renderer side
// (Chronon3D LensModel + PhysicalLensProjection) consumes the same numbers,
// so this file is the single mm<->fov table both sides share.

// Full-frame gate the AE presets are defined against.
const (
	lensGateWidthMM  = 36.0
	lensGateHeightMM = 24.0
)

// AELensPreset is one selectable AE camera lens.
type AELensPreset struct {
	FocalMM     float64
	Name        string
	Description string
}

// AELensPresets is the canonical AE family. 50mm is the default preset: it is
// what the human eye sees (~35-50mm) and what AE preselects.
func AELensPresets() []AELensPreset {
	return []AELensPreset{
		{FocalMM: 24, Name: "24mm", Description: "wide: exaggerated parallax, narrow-face look"},
		{FocalMM: 28, Name: "28mm", Description: "wide-normal"},
		{FocalMM: 35, Name: "35mm", Description: "normal-wide, human-eye lower bound"},
		{FocalMM: 50, Name: "50mm", Description: "default: human-eye normal lens"},
		{FocalMM: 85, Name: "85mm", Description: "telephoto: compressed depth, portrait lens"},
	}
}

// IsAELensPreset reports whether mm is one of the canonical presets.
func IsAELensPreset(mm float64) bool {
	for _, p := range AELensPresets() {
		if p.FocalMM == mm {
			return true
		}
	}
	return false
}

// FOVDegFromFocalMM converts a focal length on the full-frame gate to the
// vertical field of view in degrees: fov = 2*atan(sensorH / 2f).
func FOVDegFromFocalMM(focalMM float64) (float64, error) {
	if !(focalMM >= 12 && focalMM <= 200) {
		return 0, fmt.Errorf("overlay: focal_length_mm %v is outside [12, 200]", focalMM)
	}
	return 2 * math.Atan(lensGateHeightMM/(2*focalMM)) * 180 / math.Pi, nil
}

// FocalMMFromFOVDeg is the inverse of FOVDegFromFocalMM.
func FocalMMFromFOVDeg(fovDeg float64) (float64, error) {
	if !(fovDeg >= 10 && fovDeg <= 120) {
		return 0, fmt.Errorf("overlay: fov_deg %v is outside [10, 120]", fovDeg)
	}
	return (lensGateHeightMM / 2) / math.Tan(fovDeg*math.Pi/360), nil
}

// DisperseDepths is the Depth-script equivalent: N layers fanned out in
// Z-space from baseZ in steps of spacing (positive spacing pushes later
// layers further back, matching AE +Z-away convention used by the promo
// rig: scene gaps 3000/1500/0, intra-scene card gaps of 250px).
func DisperseDepths(count int, baseZ, spacing float64) ([]float64, error) {
	if count < 1 {
		return nil, fmt.Errorf("overlay: disperse needs at least 1 layer, got %d", count)
	}
	if !isFinite(baseZ) || !isFinite(spacing) {
		return nil, fmt.Errorf("overlay: disperse base_z/spacing must be finite")
	}
	out := make([]float64, count)
	for i := range out {
		out[i] = baseZ + float64(i)*spacing
	}
	return out, nil
}

// ScaleToPreserveApparentSize returns the scale multiplier that keeps a
// layer's apparent size constant when it moves from zOld to zNew at a fixed
// camera distance: apparent size scales as 1/|z|, so scale *= |zNew|/|zOld|.
// This is what the Depth script bakes automatically (a layer pushed back
// would otherwise shrink); AE authors the same compensation by hand.
func ScaleToPreserveApparentSize(zOld, zNew float64) (float64, error) {
	if zOld == 0 || zNew == 0 {
		return 0, fmt.Errorf("overlay: apparent-size compensation needs nonzero depths, got %v -> %v", zOld, zNew)
	}
	if !isFinite(zOld) || !isFinite(zNew) {
		return 0, fmt.Errorf("overlay: apparent-size compensation needs finite depths")
	}
	return math.Abs(zNew) / math.Abs(zOld), nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
