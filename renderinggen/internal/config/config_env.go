// config_env.go owns the ONE environment → configuration overlay.
//
// Why it exists. Environment knobs were read pointwise with os.Getenv from the
// packages that happened to need them (CHRONON_STALL_TIMEOUT in the chronon
// client, CHRONON_HOME in six places, RENDERINGGEN_* scattered over the
// processor), so the set of deployable settings was discoverable only by
// grepping, was not validated (a bogus value fell back to the default with a log
// line at best), and could not be exercised by the config tests. Here the env
// key is DERIVED from the configuration itself — the uppercased yaml path under
// the RENDERINGGEN_ prefix — so a new setting is env-overridable the moment it
// is declared, and an invalid value fails Load with the variable name and the
// expected format instead of silently degrading the worker.
//
// Precedence is documented in config.go: defaults < file < environment.
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// EnvPrefix is the single prefix under which every worker setting can be
// overridden.
const EnvPrefix = "RENDERINGGEN_"

// durationType is the type the scalar setter parses with time.ParseDuration
// rather than strconv.
var durationType = reflect.TypeOf(time.Duration(0))

// envAliases maps a PRE-EXISTING environment name onto the canonical
// RENDERINGGEN_* key it is now an alias of.
//
// Each entry is a compatibility promise to deployments that already set the old
// name, so it stays until the variable is retired. The canonical key always
// wins when both are set: the alias is a fallback, never an override of the
// documented name.
var envAliases = map[string]string{
	// Read by internal/chronon before the stall timeout became a setting.
	"CHRONON_STALL_TIMEOUT": EnvPrefix + "CHRONON_STALL_TIMEOUT",
	// Read pointwise inside internal/processor before these became settings.
	// The short names are kept because deployments already set them; the
	// canonical key derived from the yaml path always wins when both are set.
	"RENDERINGGEN_RECEIPT_VERIFY": EnvPrefix + "PIPELINE_RECEIPT_VERIFY",
	"RENDERINGGEN_DEEP_VISUAL":    EnvPrefix + "PIPELINE_DEEP_VISUAL_VALIDATION",
	"RENDERINGGEN_KEEP_WORKSPACE": EnvPrefix + "PIPELINE_KEEP_WORKSPACE",
}

// applyEnvOverrides walks the configuration and applies every RENDERINGGEN_*
// variable that is set. lookup is injectable so tests pin the precedence rules
// without touching the process environment.
func applyEnvOverrides(c *Config) error {
	return applyEnvToValue(reflect.ValueOf(c).Elem(), nil, os.LookupEnv)
}

// applyEnvToValue applies the overlay to one struct value. path accumulates the
// yaml tags on the way down and becomes the env key, so the mapping is derived
// from the configuration instead of being maintained beside it.
func applyEnvToValue(v reflect.Value, path []string, lookup func(string) (string, bool)) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			// A field without a documented yaml name has no derivable env key,
			// which would silently make it the one setting an operator cannot
			// change. Fail loudly instead.
			return fmt.Errorf("config: %s.%s has no yaml tag, so no environment override can be derived for it", t.Name(), field.Name)
		}
		next := append(append([]string(nil), path...), tag)
		fieldValue := v.Field(i)
		if fieldValue.Kind() == reflect.Struct {
			if err := applyEnvToValue(fieldValue, next, lookup); err != nil {
				return err
			}
			continue
		}
		envName := EnvPrefix + strings.ToUpper(strings.Join(next, "_"))
		raw, ok := lookupEnvWithAliases(envName, lookup)
		if !ok || raw == "" {
			continue
		}
		if err := setConfigScalar(fieldValue, raw, envName); err != nil {
			return err
		}
	}
	return nil
}

// lookupEnvWithAliases resolves envName, falling back to the legacy names that
// alias it.
func lookupEnvWithAliases(envName string, lookup func(string) (string, bool)) (string, bool) {
	if raw, ok := lookup(envName); ok {
		return raw, true
	}
	for legacy, canonical := range envAliases {
		if canonical != envName {
			continue
		}
		if raw, ok := lookup(legacy); ok {
			return raw, true
		}
		break
	}
	return "", false
}

// setConfigScalar parses one environment value into its configuration field.
//
// Every failure names the variable and the accepted format: the previous
// arrangement (a package-local os.Getenv with a log line and a silent fallback)
// meant a typo produced a worker that ran with defaults nobody intended, with no
// way to see it from the configuration.
func setConfigScalar(field reflect.Value, raw, envName string) error {
	if field.Type() == durationType {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("config: %s=%q is not a duration (write it as a Go duration, e.g. \"25s\" or \"10m\"): %w", envName, raw, err)
		}
		field.SetInt(int64(parsed))
		return nil
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("config: %s=%q is not a boolean (true/false): %w", envName, raw, err)
		}
		field.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("config: %s=%q is not an integer: %w", envName, raw, err)
		}
		if field.OverflowInt(parsed) {
			return fmt.Errorf("config: %s=%q overflows %s", envName, raw, field.Type())
		}
		field.SetInt(parsed)
	default:
		// Reachable only when such a variable is actually set, so declaring a
		// non-scalar setting later is not an error on its own — trying to
		// override it is.
		return fmt.Errorf("config: %s cannot override a %s setting; only string, bool, int and duration settings are env-overridable", envName, field.Kind())
	}
	return nil
}
