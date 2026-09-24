package motion

// The canonical catalog is NOT owned here.
//
// ChrononTemplate owns the vocabulary — its `catalog/motion_catalog.v1.json`
// data file plus the template and composition-preset lists its C++ enums define
// — and its `chronontemplate_emit_catalog` tool is the only writer of the
// emitted artifact this package embeds:
//
//	ChrononTemplate/catalog/motion_catalog.v1.json   ids, keyframes, selections
//	ChrononTemplate/tools/emit_catalog.cpp           validation + C++ lists
//	                 │  chronontemplate_emit_catalog
//	                 ▼
//	internal/motion/catalog/chronontemplate_catalog.v1.json   ← embedded here
//
// This file therefore owns exactly one thing: reading that artifact and failing
// closed when it cannot. The motion family files that used to build the
// vocabulary in Go (catalog_layer.go, catalog_text.go, catalog_phrase*.go,
// catalog_typewriter.go, resolver.go's LegacyDefinition) are gone, so there is
// no second place where a motion id, its keyframes or its family can drift.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed catalog/chronontemplate_catalog.v1.json
var canonicalCatalogJSON []byte

// canonicalCatalogSchemaVersion is the only artifact revision this build can
// read. A newer catalog is a build-ordering mistake, not something to
// best-effort parse.
const canonicalCatalogSchemaVersion = 1

// canonicalCatalogSource is the producer named in the artifact. It is checked
// because "some JSON with motions in it" is not the canonical catalog.
const canonicalCatalogSource = "ChrononTemplate"

// CatalogTemplate is one C++-owned social/web template the emitter listed.
type CatalogTemplate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CatalogMaterial carries only the two facts that decide whether the declared
// halo is active: a halo exists exactly when the material is emissive with a
// positive strength. The colour itself stays in the C++ material, so the
// artifact does not become a second colour authority.
type CatalogMaterial struct {
	Kind             string  `json:"kind"`
	EmissiveStrength float64 `json:"emissive_strength"`
}

// CatalogPreset is one C++-owned composition preset the emitter listed.
type CatalogPreset struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Material CatalogMaterial `json:"material"`
}

// CatalogPhraseOverlay is one corpus row: a phrase overlay rendered through one
// motion. The corpus itself is a RenderingGen concern, but the ids it names are
// not, so it is validated against this catalog instead of being trusted.
type CatalogPhraseOverlay struct {
	ID     string `json:"id"`
	Motion string `json:"motion"`
}

// CatalogSelections are the named corpus selections the batch builders request
// by name instead of restating the ids in Go.
type CatalogSelections struct {
	TysonPhraseMotions   []string               `json:"tyson_phrase_motions"`
	PhraseMotionPool     []string               `json:"phrase_motion_pool"`
	MatrixPhraseOverlays []CatalogPhraseOverlay `json:"matrix_phrase_overlays"`
	MatrixImageOverlays  []string               `json:"matrix_image_overlays"`
}

// Catalog is the parsed canonical catalog.
type Catalog struct {
	SchemaVersion  int                 `json:"schema_version"`
	Source         string              `json:"source"`
	Templates      []CatalogTemplate   `json:"templates"`
	Final3DPresets []CatalogPreset     `json:"final3d_presets"`
	Motions        []MotionDefinition  `json:"motions"`
	OverlayPresets map[string][]string `json:"overlay_presets"`
	Selections     CatalogSelections   `json:"selections"`
}

var (
	canonicalOnce  sync.Once
	canonicalValue Catalog
	canonicalError error
)

// Canonical returns the catalog emitted by ChrononTemplate. The artifact is
// parsed once and validated once; every later caller gets the same value (or
// the same error).
func Canonical() (Catalog, error) {
	canonicalOnce.Do(func() { canonicalValue, canonicalError = loadCanonical() })
	return canonicalValue, canonicalError
}

func loadCanonical() (Catalog, error) {
	return parseCanonical(canonicalCatalogJSON)
}

// parseCanonical decodes and validates one canonical catalog document. It is
// split out from loadCanonical so the gates can be exercised against a mutated
// document instead of only against the healthy embedded one — a validation that
// never sees a broken input is not evidence that it rejects one.
func parseCanonical(raw []byte) (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("motion: decode canonical catalog: %w", err)
	}
	if catalog.SchemaVersion != canonicalCatalogSchemaVersion {
		return Catalog{}, fmt.Errorf("motion: canonical catalog schema_version %d, expected %d",
			catalog.SchemaVersion, canonicalCatalogSchemaVersion)
	}
	if catalog.Source != canonicalCatalogSource {
		return Catalog{}, fmt.Errorf("motion: canonical catalog source %q, expected %q",
			catalog.Source, canonicalCatalogSource)
	}
	if len(catalog.Motions) == 0 {
		return Catalog{}, fmt.Errorf("motion: canonical catalog defines no motions")
	}
	// The template and composition-preset sections are owned by ChrononTemplate
	// and are not rendered by this repository; they are still validated here
	// because an emitter that lost either section produced a truncated artifact,
	// and consuming four fifths of a catalog is not a partial success.
	for _, preset := range catalog.Final3DPresets {
		if err := validatePresetMaterial(preset); err != nil {
			return Catalog{}, err
		}
	}

	for _, section := range []struct {
		name string
		ids  []string
	}{
		{"templates", templateIDs(catalog.Templates)},
		{"final3d_presets", presetIDs(catalog.Final3DPresets)},
	} {
		if len(section.ids) == 0 {
			return Catalog{}, fmt.Errorf("motion: canonical catalog declares no %s", section.name)
		}
		seen := make(map[string]struct{}, len(section.ids))
		for _, id := range section.ids {
			if strings.TrimSpace(id) == "" {
				return Catalog{}, fmt.Errorf("motion: canonical catalog %s section has an empty id", section.name)
			}
			if _, duplicate := seen[id]; duplicate {
				return Catalog{}, fmt.Errorf("motion: canonical catalog repeats %s id %q", section.name, id)
			}
			seen[id] = struct{}{}
		}
	}

	motionIDs := make(map[string]struct{}, len(catalog.Motions))
	for _, definition := range catalog.Motions {
		if err := ValidateDefinition(definition); err != nil {
			return Catalog{}, fmt.Errorf("motion: canonical catalog: %w", err)
		}
		if _, duplicate := motionIDs[definition.ID]; duplicate {
			return Catalog{}, fmt.Errorf("motion: canonical catalog repeats motion %q", definition.ID)
		}
		motionIDs[definition.ID] = struct{}{}
	}

	imagePresets := make(map[string]struct{})
	for family, ids := range catalog.OverlayPresets {
		if len(ids) == 0 {
			return Catalog{}, fmt.Errorf("motion: canonical catalog lists no %q overlay presets", family)
		}
		if family == "image" {
			for _, id := range ids {
				imagePresets[id] = struct{}{}
			}
		}
	}
	for _, required := range []string{"text", "image"} {
		if len(catalog.OverlayPresets[required]) == 0 {
			return Catalog{}, fmt.Errorf("motion: canonical catalog is missing the %q overlay preset family", required)
		}
	}

	for _, id := range catalog.Selections.TysonPhraseMotions {
		if _, ok := motionIDs[id]; !ok {
			return Catalog{}, fmt.Errorf("motion: canonical catalog selection tyson_phrase_motions names unknown motion %q", id)
		}
	}
	if len(catalog.Selections.PhraseMotionPool) == 0 {
		return Catalog{}, fmt.Errorf("motion: canonical catalog selection phrase_motion_pool is empty")
	}
	phrasePoolSeen := make(map[string]struct{}, len(catalog.Selections.PhraseMotionPool))
	for _, id := range catalog.Selections.PhraseMotionPool {
		if _, ok := motionIDs[id]; !ok {
			return Catalog{}, fmt.Errorf("motion: canonical catalog selection phrase_motion_pool names unknown motion %q", id)
		}
		if _, duplicate := phrasePoolSeen[id]; duplicate {
			return Catalog{}, fmt.Errorf("motion: canonical catalog selection phrase_motion_pool repeats motion %q", id)
		}
		phrasePoolSeen[id] = struct{}{}
	}
	for _, row := range catalog.Selections.MatrixPhraseOverlays {
		if _, ok := motionIDs[row.Motion]; !ok {
			return Catalog{}, fmt.Errorf("motion: canonical catalog selection matrix_phrase_overlays names unknown motion %q", row.Motion)
		}
	}
	for _, id := range catalog.Selections.MatrixImageOverlays {
		if _, ok := imagePresets[id]; !ok {
			return Catalog{}, fmt.Errorf("motion: canonical catalog selection matrix_image_overlays names unknown image preset %q", id)
		}
	}

	return catalog, nil
}

// OverlayPresetIDs returns the canonical overlay preset ids for one family
// ("text" or "image"), sorted, or nil when the catalog does not define that
// family. The overlay preset definitions themselves stay in `internal/overlay`
// — those are rendering style, not a catalog — which is why the two halves are
// checked against each other by overlay.ValidateCatalogParity instead of being
// merged into one file.
func OverlayPresetIDs(family string) []string {
	catalog, err := Canonical()
	if err != nil {
		return nil
	}
	ids := append([]string(nil), catalog.OverlayPresets[family]...)
	sort.Strings(ids)
	return ids
}

// PhraseMotions returns the corpus phrase-motion selection: one motion id per
// phrase overlay, in the order the catalog pairs them.
func PhraseMotions() []string {
	catalog, err := Canonical()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(catalog.Selections.TysonPhraseMotions))
	return append(ids, catalog.Selections.TysonPhraseMotions...)
}

// PhraseMotionPool returns the catalog-owned set of GPU-native entrance motions
// eligible for deterministic phrase selection.
func PhraseMotionPool() []string {
	catalog, err := Canonical()
	if err != nil {
		return nil
	}
	return append([]string(nil), catalog.Selections.PhraseMotionPool...)
}

// PhraseOverlays returns the multilingual phrase-overlay rows (overlay id plus
// the motion it renders through) in the order the catalog declares them.
func PhraseOverlays() []CatalogPhraseOverlay {
	catalog, err := Canonical()
	if err != nil {
		return nil
	}
	return append([]CatalogPhraseOverlay(nil), catalog.Selections.MatrixPhraseOverlays...)
}

// ImageOverlays returns the image-overlay matrix selection, in catalog order.
func ImageOverlays() []string {
	catalog, err := Canonical()
	if err != nil {
		return nil
	}
	return append([]string(nil), catalog.Selections.MatrixImageOverlays...)
}

func validatePresetMaterial(preset CatalogPreset) error {
	switch preset.Material.Kind {
	case "unlit", "lambert":
		return nil
	case "emissive":
		if preset.Material.EmissiveStrength > 0 {
			return nil
		}
		return fmt.Errorf("motion: preset %q is emissive with non-positive emissive_strength %v",
			preset.ID, preset.Material.EmissiveStrength)
	default:
		return fmt.Errorf("motion: preset %q declares unknown material kind %q", preset.ID, preset.Material.Kind)
	}
}

func templateIDs(templates []CatalogTemplate) []string {
	ids := make([]string, 0, len(templates))
	for _, template := range templates {
		ids = append(ids, template.ID)
	}
	return ids
}

func presetIDs(presets []CatalogPreset) []string {
	ids := make([]string, 0, len(presets))
	for _, preset := range presets {
		ids = append(ids, preset.ID)
	}
	return ids
}

// init registers the canonical motion vocabulary. A catalog that cannot be read
// or validated stops the process: every motion would otherwise resolve to
// nothing and every overlay would render as empty pixels.
func init() {
	catalog, err := Canonical()
	if err != nil {
		panic(err)
	}
	for _, definition := range catalog.Motions {
		if err := Registry.Register(definition.ID, DeclarativePlugin{Definition: definition}); err != nil {
			panic(err)
		}
	}
}
