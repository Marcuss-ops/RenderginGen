// Package dateanim is the RenderingGen-side catalog and selector for complete
// multi-date animations.
//
// The visual plans live in ChrononTemplate. This package only carries the
// style vocabulary RenderingGen needs when a plan has more than one date:
// the 13 approved styles, their fixed authoring font sizes, and a stable
// random selector. It deliberately does not invent a Chronon plan.
package dateanim

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed catalog.v1.json
var embeddedCatalog embed.FS

const (
	CatalogSchema  = "renderinggen.date-animation-catalog.v1"
	CatalogVersion = 1
	expectedStyles = 13
)

type Selection struct {
	Strategy        string `json:"strategy"`
	Salt            string `json:"salt"`
	MinimumDates    int    `json:"minimum_dates"`
	SingleDateStyle string `json:"single_date_style"`
}

type Style struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	FontSize float64 `json:"font_size"`
	FPS      int     `json:"fps"`
	Frames   int     `json:"frames"`
	Plan     string  `json:"plan"`
	Video    string  `json:"video"`
}

type Catalog struct {
	Schema    string    `json:"schema"`
	Version   int       `json:"version"`
	Source    string    `json:"source"`
	Labels    bool      `json:"labels"`
	Selection Selection `json:"selection"`
	Styles    []Style   `json:"styles"`
}

var cached *Catalog

// Load returns the embedded catalog after validating its closed vocabulary.
// Validation is fail-closed: a duplicated style, a removed 14/15 entry, a
// reintroduced label flag or an unset font size is a build/runtime error, not
// something a caller may paper over.
func Load() (Catalog, error) {
	if cached != nil {
		return *cached, nil
	}
	raw, err := embeddedCatalog.ReadFile("catalog.v1.json")
	if err != nil {
		return Catalog{}, fmt.Errorf("dateanim: read catalog: %w", err)
	}
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("dateanim: decode catalog: %w", err)
	}
	if catalog.Schema != CatalogSchema || catalog.Version != CatalogVersion {
		return Catalog{}, fmt.Errorf("dateanim: unsupported catalog schema/version %q/%d", catalog.Schema, catalog.Version)
	}
	if catalog.Labels {
		return Catalog{}, fmt.Errorf("dateanim: labels must be disabled")
	}
	if len(catalog.Styles) != expectedStyles {
		return Catalog{}, fmt.Errorf("dateanim: expected %d styles, got %d", expectedStyles, len(catalog.Styles))
	}
	if catalog.Selection.Strategy != "stable_random" || catalog.Selection.Salt == "" ||
		catalog.Selection.MinimumDates != 2 || catalog.Selection.SingleDateStyle == "" {
		return Catalog{}, fmt.Errorf("dateanim: invalid selection policy")
	}
	seen := make(map[string]bool, len(catalog.Styles))
	for _, style := range catalog.Styles {
		if style.ID == "" || style.Title == "" || style.Plan == "" || style.Video == "" {
			return Catalog{}, fmt.Errorf("dateanim: style has an empty identity field")
		}
		if strings.HasPrefix(style.ID, "14_") || strings.HasPrefix(style.ID, "15_") {
			return Catalog{}, fmt.Errorf("dateanim: removed style %q is still in the catalog", style.ID)
		}
		if seen[style.ID] {
			return Catalog{}, fmt.Errorf("dateanim: duplicate style %q", style.ID)
		}
		if style.FontSize <= 0 || style.FPS <= 0 || style.Frames <= 0 {
			return Catalog{}, fmt.Errorf("dateanim: style %q has invalid font/fps/frames", style.ID)
		}
		seen[style.ID] = true
	}
	if !seen[catalog.Selection.SingleDateStyle] {
		return Catalog{}, fmt.Errorf("dateanim: single-date style %q is not in the catalog", catalog.Selection.SingleDateStyle)
	}
	cached = &catalog
	return catalog, nil
}

// StyleIDs returns the closed style IDs in sorted order. Callers use this list
// for pickers and for the stable selector; it is never inferred from filenames.
func StyleIDs() []string {
	catalog, err := Load()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(catalog.Styles))
	for _, style := range catalog.Styles {
		ids = append(ids, style.ID)
	}
	sort.Strings(ids)
	return ids
}

// StyleByID returns one catalog style by its stable ID.
func StyleByID(id string) (Style, bool) {
	catalog, err := Load()
	if err != nil {
		return Style{}, false
	}
	for _, style := range catalog.Styles {
		if style.ID == id {
			return style, true
		}
	}
	return Style{}, false
}
