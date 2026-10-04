package overlay

import (
	"encoding/json"
	"fmt"
	"math"
)

func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case json.Number:
		f, err := n.Float64()
		return f, err
	default:
		return 0, fmt.Errorf("must be a number, got %T", v)
	}
}

func toInt(v any) (int, error) {
	f, err := toFloat(v)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || f < float64(math.MinInt) || f > float64(math.MaxInt) {
		return 0, fmt.Errorf("must be a finite integer, got %v", v)
	}
	return int(f), nil
}
