// Package numeric owns the small numeric predicates shared by the renderer's
// validation and lowering layers.
package numeric

import "math"

// Finite reports whether value is neither NaN nor an infinity.
func Finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
