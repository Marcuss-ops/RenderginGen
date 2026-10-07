package motion

import (
	"math"
)

func finiteMotionValue(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func motionNumericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, finiteMotionValue(number)
	case float32:
		result := float64(number)
		return result, finiteMotionValue(result)
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	default:
		return 0, false
	}
}

func motionNumericVector(value any) ([]float64, bool) {
	switch values := value.(type) {
	case []any:
		out := make([]float64, len(values))
		for i, item := range values {
			number, ok := motionNumericValue(item)
			if !ok {
				return nil, false
			}
			out[i] = number
		}
		return out, true
	case []float64:
		for _, number := range values {
			if !finiteMotionValue(number) {
				return nil, false
			}
		}
		return values, true
	default:
		return nil, false
	}
}

func validMotionEasing(easing string) bool {
	switch easing {
	case "linear", "in_quad", "out_quad", "in_out_quad", "in_cubic", "out_cubic", "in_out_cubic",
		"in_expo", "out_expo", "in_out_expo", "in_sine", "out_sine", "in_out_sine",
		"in_back", "out_back", "in_out_back", "in_elastic", "out_elastic", "in_out_elastic",
		"in_bounce", "out_bounce", "in_out_bounce", "smoothstep", "hold":
		return true
	default:
		return false
	}
}

func finiteMotionValueTree(value any) bool {
	switch item := value.(type) {
	case float64:
		return finiteMotionValue(item)
	case float32:
		return finiteMotionValue(float64(item))
	case []any:
		for _, child := range item {
			if !finiteMotionValueTree(child) {
				return false
			}
		}
	case []float64:
		for _, child := range item {
			if !finiteMotionValue(child) {
				return false
			}
		}
	case map[string]any:
		for _, child := range item {
			if !finiteMotionValueTree(child) {
				return false
			}
		}
	}
	return true
}

func containsMotionString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func validMotionHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, r := range value[1:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
