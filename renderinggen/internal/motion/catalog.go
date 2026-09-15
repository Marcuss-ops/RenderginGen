package motion

// The built-in motion catalog is organized as one file per AUTHORING FAMILY so
// a reader sees one vocabulary at a time:
//
//	catalog_layer.go        whole-layer transforms (legacy + spring/scale reveals)
//	catalog_text.go         per-word/glyph reveal, tracking and wave vocabulary
//	catalog_phrase.go       the apple_v2 editorial phrase library (helpers + classic set)
//	catalog_phrase_modern.go the additive modern v3 phrase batch
//	catalog_typewriter.go   the per-character typewriter family
//
// The split is authoring organisation only. Registration below is the SINGLE
// place that touches Registry, in a deterministic family order, so there is
// still exactly one registry and one lookup: no family can shadow another, and
// adding a definition is a local edit inside the family it belongs to.
func init() {
	for _, family := range [][]MotionDefinition{
		layerMotions(),
		textMotions(),
		phraseMotions(),
		typewriterMotions(),
	} {
		for _, definition := range family {
			_ = Registry.Register(definition.ID, DeclarativePlugin{Definition: definition})
		}
	}
}
