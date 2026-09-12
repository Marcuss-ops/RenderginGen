// registry.go is the SINGLE owner of the overlay item-kind and
// template_id → TemplateSpec mapping.
//
// Before this file three views kept their own hardcoded template lists — the
// preset requirement, the two-layer entity lowering and the artifact ledger
// counters — and they could disagree (a person and a place were entities for
// one view and invisible to the others). They now all read templateRegistry,
// so an item is always lowered, validated and counted with the same spec.
//
// The contract:
//
//	kind        → semantic behaviour (WHAT the item is)       — the SSOT
//	template_id → the visual template/style (HOW it is drawn)
//	preset_id   → the selected visual variant
//
// item.Kind is authoritative when the producer sends it; when it is absent the
// registry's Kind is authoritative. A present kind that contradicts the
// template's kind is rejected fail-closed — RenderingGen never guesses.
package overlay

import (
	"fmt"
	"strings"
)

// ItemKind is the semantic kind of an overlay item. It is the single
// discriminator the compiler and the ledger switch on.
type ItemKind string

const (
	KindEntityCard      ItemKind = "entity_card"
	KindOrganization    ItemKind = "organization"
	KindLocation        ItemKind = "location"
	KindConcept         ItemKind = "concept"
	KindLowerThird      ItemKind = "lower_third"
	KindImagePopup      ItemKind = "image_popup"
	KindQuote           ItemKind = "quote"
	KindNumber          ItemKind = "number"
	KindProduct         ItemKind = "product"
	KindLogo            ItemKind = "logo"
	KindEntityImage     ItemKind = "entity_image"
	KindImportantPhrase ItemKind = "important_phrase"
	KindImportantWord   ItemKind = "important_word"
	KindLightLeak       ItemKind = "light_leak"
	// KindPrimitive is the kind of a template RenderingGen does not know: a
	// preset-less text primitive whose behaviour follows the generic text
	// lowering. The producer's kind (when present) is authoritative for it.
	KindPrimitive ItemKind = "primitive"
)

// overlayStatKind is the artifact-ledger bucket a template is counted as.
type overlayStatKind uint8

const (
	_ overlayStatKind = iota
	overlayStatEntity
	overlayStatPhrase
	overlayStatWord
	overlayStatImage
	overlayStatLightLeak
)

// TemplateSpec is one row of the template registry. It is the only place that
// decides which kind a template belongs to, whether it needs a preset, which
// preset family it lowers into, and which ledger bucket it counts as. It does
// NOT decide how the layer is compiled — that is the compiler's per-kind job.
type TemplateSpec struct {
	// ID is the template_id verbatim (registry keys are normalized upper).
	ID string
	// Registered reports whether the template_id resolved to a registry row
	// (true) or fell through to the unknown primitive (false). An unknown
	// template still compiles — the historical documents and the alias path
	// must keep working — but the fall-through is no longer silent: the compile
	// pass reports it (CompileResult.UnknownTemplates) and the worker records it
	// as a metric. Without that, a producer rename (or the deletion of a
	// compatibility alias) degraded every affected entity to a bare text
	// primitive with no error anywhere.
	Registered bool
	// Kind is the semantic behaviour this template lowers through.
	Kind ItemKind
	// RequiresPreset is true when PipelineGen must supply a preset_id.
	RequiresPreset bool
	// Family is the official preset catalog family the template resolves in.
	Family PresetFamily
	// Stat is the artifact-ledger bucket the template is counted as.
	Stat overlayStatKind
}

// templateRegistry keys are the canonical, upper-cased template_id values.
// RenderingGen owns this vocabulary; the historical organization/place
// spellings are normalized once by canonicalTemplateID, so the registry holds
// exactly one key per concept.
var templateRegistry = map[string]TemplateSpec{
	// Entity cards — PipelineGen's NER output.
	"PERSON":               {Kind: KindEntityCard, RequiresPreset: true, Family: PresetText, Stat: overlayStatEntity},
	"PERSON_DEFAULT":       {Kind: KindEntityCard, Family: PresetText, Stat: overlayStatEntity},
	"ORGANIZATION":         {Kind: KindOrganization, RequiresPreset: true, Family: PresetText, Stat: overlayStatEntity},
	"ORGANIZATION_DEFAULT": {Kind: KindOrganization, Family: PresetText, Stat: overlayStatEntity},
	"LOCATION":             {Kind: KindLocation, RequiresPreset: true, Family: PresetText, Stat: overlayStatEntity},
	"LOCATION_DEFAULT":     {Kind: KindLocation, Family: PresetText, Stat: overlayStatEntity},
	"CONCEPT":              {Kind: KindConcept, RequiresPreset: true, Family: PresetText, Stat: overlayStatPhrase},
	"CONCEPT_DEFAULT":      {Kind: KindConcept, Family: PresetText, Stat: overlayStatPhrase},
	"LOWER_THIRD":          {Kind: KindLowerThird, Family: PresetText, Stat: overlayStatPhrase},

	// Important phrases.
	"IMPORTANT_PHRASE": {Kind: KindImportantPhrase, RequiresPreset: true, Family: PresetText, Stat: overlayStatPhrase},
	"QUOTE":            {Kind: KindQuote, RequiresPreset: true, Family: PresetText, Stat: overlayStatPhrase},

	// Important words and figures.
	"IMPORTANT_WORD": {Kind: KindImportantWord, RequiresPreset: true, Family: PresetText, Stat: overlayStatWord},
	"NUMBER":         {Kind: KindNumber, RequiresPreset: true, Family: PresetText, Stat: overlayStatWord},
	"MONEY":          {Kind: KindNumber, RequiresPreset: true, Family: PresetText, Stat: overlayStatWord},
	"PERCENT":        {Kind: KindNumber, RequiresPreset: true, Family: PresetText, Stat: overlayStatWord},

	// Image overlays.
	"IMAGE_OVERLAY": {Kind: KindEntityImage, RequiresPreset: true, Family: PresetImage, Stat: overlayStatImage},
	"IMAGE_ENTITY":  {Kind: KindEntityImage, RequiresPreset: true, Family: PresetImage, Stat: overlayStatImage},
	"IMAGE_POPUP":   {Kind: KindImagePopup, Family: PresetImage, Stat: overlayStatImage},
	"PRODUCT":       {Kind: KindProduct, Family: PresetImage, Stat: overlayStatImage},
	"LOGO":          {Kind: KindLogo, Family: PresetImage, Stat: overlayStatImage},
	"LIGHT_LEAK":    {Kind: KindLightLeak, Family: PresetImage, Stat: overlayStatLightLeak},
}

// legacyTemplateAliases resolve the migrated organization/place spellings ONCE
// at the registry boundary. PipelineGen historically emitted the lowercase
// aliases; after the migration it emits the canonical ids, but the aliases
// stay so historical documents keep their kind instead of degrading to a bare
// primitive. Keys are lower-case so the canonical uppercase id stays the only
// canonical spelling in the registry.
var legacyTemplateAliases = map[string]string{
	"org_default": "ORGANIZATION_DEFAULT",
	"gpe_default": "LOCATION_DEFAULT",
}

// canonicalTemplateID normalizes a producer template_id to the canonical
// registry key: uppercase, with the migrated aliases resolved in one place.
func canonicalTemplateID(templateID string) string {
	trimmed := strings.TrimSpace(templateID)
	if canonical, ok := legacyTemplateAliases[strings.ToLower(trimmed)]; ok {
		return canonical
	}
	return strings.ToUpper(trimmed)
}

// templateSpecFor returns the registry spec for a template_id. Unknown
// templates are preset-less text primitives: text family, no preset
// requirement, no ledger counter.
func templateSpecFor(templateID string) TemplateSpec {
	if spec, ok := templateRegistry[canonicalTemplateID(templateID)]; ok {
		spec.ID = templateID
		spec.Registered = true
		return spec
	}
	return TemplateSpec{ID: templateID, Kind: KindPrimitive, Family: PresetText}
}

// kindBehavior is the coarse rendering behaviour a kind selects. The compiler
// dispatches on it; the producer's exact kind vocabulary is richer than the
// three behaviours RenderingGen needs.
type kindBehavior uint8

const (
	behaviorText kindBehavior = iota
	behaviorEntity
	behaviorImage
)

// behaviorOf maps every accepted kind (including PipelineGen synonyms such as
// "image", "keyword", "text_phrase") to its rendering behaviour.
func behaviorOf(kind ItemKind) kindBehavior {
	switch kind {
	case KindEntityCard, KindOrganization, KindLocation, KindConcept:
		return behaviorEntity
	case KindEntityImage, KindImagePopup, KindProduct, KindLogo, KindLightLeak, ItemKind("image"):
		return behaviorImage
	default:
		return behaviorText
	}
}

// resolveKind makes the item's kind authoritative while the registry backs it
// up. An empty kind takes the registry's kind; a present kind whose behaviour
// contradicts a known template's behaviour is rejected fail-closed. The check
// compares behaviour, not exact spelling, so PipelineGen's synonym vocabulary
// ("image" for IMAGE_OVERLAY, "keyword" for IMPORTANT_WORD, "text_phrase" for
// IMPORTANT_PHRASE) stays valid while a genuinely wrong pairing — e.g. a
// phrase kind on the PERSON entity template — is still rejected.
func (spec TemplateSpec) resolveKind(itemKind, itemID string) (ItemKind, error) {
	kind := ItemKind(strings.ToLower(strings.TrimSpace(itemKind)))
	if kind == "" {
		return spec.Kind, nil
	}
	// Unknown templates are not governed by the registry: the producer's kind
	// is authoritative for them.
	if spec.Kind == KindPrimitive {
		return kind, nil
	}
	if behaviorOf(kind) != behaviorOf(spec.Kind) {
		return "", fmt.Errorf("overlay: item %q template %q is %s, received kind %q", itemID, spec.ID, spec.Kind, kind)
	}
	return kind, nil
}

// isEntityKind reports whether a kind lowers to the entity-card composition:
// an image card plus its display-text card when an asset is present.
func isEntityKind(kind ItemKind) bool { return behaviorOf(kind) == behaviorEntity }

// isImageKind reports whether a kind lowers to a single image layer and
// therefore requires an asset ref.
func isImageKind(kind ItemKind) bool { return behaviorOf(kind) == behaviorImage }
