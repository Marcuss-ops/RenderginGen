// Package clampx is the single clamp authority: every overlay geometry site
// (map boxes, motion tracks, caption/pin radii) clamps through here so edge
// cases (negative limits, NaN) behave identically instead of being
// redefined per call-site.
package clampx

import "math"

// Float clamps value into [0, limit]; a negative limit becomes 0; NaN maps
// to 0 so a corrupt coordinate can never propagate as NaN geometry.
func Float(value, limit float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	if math.IsNaN(limit) || limit < 0 {
		limit = 0
	}
	return min(max(value, 0), limit)
}

// Unit clamps value into [0,1]; NaN maps to 0.
func Unit(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return min(max(value, 0), 1)
}
