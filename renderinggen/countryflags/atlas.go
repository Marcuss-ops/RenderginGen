// Package countryflags exposes the bundled country-flag atlas to RenderingGen
// callers without a network fetch or filesystem dependency at render time.
package countryflags

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

//go:embed atlas.v1.json
var atlasFS embed.FS

// Flag is a runtime-ready, rasterized national flag keyed by ISO 3166-1 alpha-2.
// ImageDataURI can be used directly by image-capable renderers.
type Flag struct {
	CountryCode  string `json:"country_code"`
	Name         string `json:"name"`
	MediaType    string `json:"media_type"`
	ImageDataURI string `json:"image_data_uri"`
}

type atlasDocument struct {
	SchemaVersion     int    `json:"schema_version"`
	CountryCodeScheme string `json:"country_code_scheme"`
	Entries           []Flag `json:"entries"`
}

var (
	loadOnce sync.Once
	byCode   map[string]Flag
	byName   map[string]Flag
	allFlags []Flag
	loadErr  error
)

func load() {
	data, err := fs.ReadFile(atlasFS, "atlas.v1.json")
	if err != nil {
		loadErr = err
		return
	}
	var document atlasDocument
	if err := json.Unmarshal(data, &document); err != nil {
		loadErr = fmt.Errorf("countryflags: decode atlas: %w", err)
		return
	}
	if document.SchemaVersion != 1 || document.CountryCodeScheme != "ISO 3166-1 alpha-2" {
		loadErr = fmt.Errorf("countryflags: unsupported atlas schema or country-code scheme")
		return
	}
	byCode = make(map[string]Flag, len(document.Entries))
	byName = make(map[string]Flag, len(document.Entries))
	for _, flag := range document.Entries {
		code := strings.ToUpper(strings.TrimSpace(flag.CountryCode))
		if len(code) != 2 || flag.MediaType != "image/png" || !strings.HasPrefix(flag.ImageDataURI, "data:image/png;base64,") {
			loadErr = fmt.Errorf("countryflags: invalid atlas entry %q", flag.CountryCode)
			return
		}
		if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(flag.ImageDataURI, "data:image/png;base64,")); err != nil {
			loadErr = fmt.Errorf("countryflags: invalid PNG data for %q: %w", code, err)
			return
		}
		flag.CountryCode = code
		byCode[code] = flag
		byName[nameKey(flag.Name)] = flag
		allFlags = append(allFlags, flag)
	}
}

func ready() error {
	loadOnce.Do(load)
	return loadErr
}

// Lookup returns the bundled flag for a two-letter ISO country code.
func Lookup(code string) (Flag, bool) {
	if ready() != nil {
		return Flag{}, false
	}
	flag, ok := byCode[strings.ToUpper(strings.TrimSpace(code))]
	return flag, ok
}

// LookupByName finds a flag by its English atlas name, ignoring case and
// whitespace, underscores, and punctuation in names supplied by map catalogs.
func LookupByName(name string) (Flag, bool) {
	if ready() != nil {
		return Flag{}, false
	}
	flag, ok := byName[nameKey(name)]
	return flag, ok
}

// PNG returns decoded PNG bytes suitable for a runtime image asset.
func PNG(code string) ([]byte, bool) {
	flag, ok := Lookup(code)
	if !ok {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(flag.ImageDataURI, "data:image/png;base64,"))
	if err != nil {
		return nil, false
	}
	return data, true
}

// Entries returns the complete stable ISO-code-sorted atlas.
func Entries() ([]Flag, error) {
	if err := ready(); err != nil {
		return nil, err
	}
	return append([]Flag(nil), allFlags...), nil
}

func nameKey(name string) string {
	var key strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			key.WriteRune(r)
		}
	}
	return key.String()
}
