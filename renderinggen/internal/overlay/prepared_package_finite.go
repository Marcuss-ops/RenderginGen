package overlay

import (
	"reflect"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/numeric"
)

func finiteFloat(value float64) bool { return numeric.Finite(value) }

// sanitizePreparedPackage replaces non-finite floats with zero.
//
// NaN cannot be encoded by encoding/json (so it broke the package content
// identity) and cannot be rendered by the GPU program. The compiler can
// produce it from degenerate animation/geometry inputs; zero is the neutral
// value for every field it can reach (opacity, offsets, scale deltas).
func sanitizePreparedPackage(pkg *PreparedPackage) {
	if pkg == nil {
		return
	}
	sanitizeNonFinite(reflect.ValueOf(pkg))
}

func sanitizeNonFinite(value reflect.Value) {
	switch value.Kind() {
	case reflect.Ptr:
		if !value.IsNil() {
			sanitizeNonFinite(value.Elem())
		}
	case reflect.Interface:
		if value.IsNil() || !value.CanInterface() {
			return
		}
		cleaned := sanitizeAnyValue(value.Interface())
		if value.CanSet() && cleaned != nil {
			value.Set(reflect.ValueOf(cleaned))
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if field.CanSet() || field.Kind() == reflect.Struct || field.Kind() == reflect.Ptr ||
				field.Kind() == reflect.Slice || field.Kind() == reflect.Map {
				sanitizeNonFinite(field)
			}
		}
	case reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			sanitizeNonFinite(value.Index(i))
		}
	case reflect.Map:
		if !value.CanSet() {
			return
		}
		for _, key := range value.MapKeys() {
			item := value.MapIndex(key)
			if item.Kind() == reflect.Interface && !item.IsNil() && item.CanInterface() {
				value.SetMapIndex(key, reflect.ValueOf(sanitizeAnyValue(item.Interface())))
			}
		}
	case reflect.Float64:
		if value.CanSet() && !finiteFloat(value.Float()) {
			value.SetFloat(0)
		}
	case reflect.Float32:
		if value.CanSet() && !finiteFloat(value.Float()) {
			value.SetFloat(0)
		}
	}
}

func sanitizeAnyValue(value any) any {
	switch typed := value.(type) {
	case float64:
		if !finiteFloat(typed) {
			return 0.0
		}
	case float32:
		if !finiteFloat(float64(typed)) {
			return float32(0)
		}
	case []any:
		for i := range typed {
			typed[i] = sanitizeAnyValue(typed[i])
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = sanitizeAnyValue(item)
		}
	}
	return value
}
