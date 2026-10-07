package overlay

import "github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"

// motionAdmitsTarget is the single overlay-side admission gate for selecting a
// catalog motion on a semantic target. Declared targets are authoritative;
// legacy compatibility is isolated here until the canonical catalog has
// explicit target metadata for every registered motion.
func motionAdmitsTarget(id, target string) bool {
	plugin, err := motion.Registry.Resolve(id)
	if err != nil || plugin == nil {
		return false
	}
	declarative, ok := plugin.(motion.DeclarativePlugin)
	if !ok {
		return false
	}
	definition := declarative.Definition

	switch target {
	case "important_phrase":
		if definition.Targets != nil {
			return containsString(definition.Targets, "text") || containsString(definition.Targets, "phrase")
		}
		return legacyPhraseMotion(id)
	case "caption":
		if definition.Targets != nil {
			return containsString(definition.Targets, "text") || containsString(definition.Targets, "phrase") || containsString(definition.Targets, "entity")
		}
		return len(definition.TextAnimators) > 0 || definition.Category == "entity_caption_v1" || definition.Category == "trump_entity_text_v1"
	case "image":
		if definition.Targets != nil {
			return containsString(definition.Targets, "image")
		}
		return definition.Category == "overlay_v3_image"
	case "map_view":
		return centeredMapMotion(definition)
	default:
		return false
	}
}

func legacyPhraseMotion(id string) bool {
	for _, family := range []string{"phrase", "typewriter", "typewriter_modern_v1", "short_phrase_style", "classic_apple", "modern_apple", "text_3d_v1", "3d", "trump_entity_text_v1"} {
		for _, candidate := range motion.Registry.FamilyMotionIDs(family) {
			if candidate == id {
				return true
			}
		}
	}
	return false
}

func centeredMapMotion(definition motion.MotionDefinition) bool {
	// Map-specific applicability has not yet been declared in ChrononTemplate.
	// Until it is, preserve only the established map_image_v1 catalog as a
	// legacy fallback; do not infer map support for arbitrary image motions.
	if definition.Targets != nil {
		if !containsString(definition.Targets, "image") || definition.Category != "map_image_v1" && definition.Category != "image_25d_clean_v1" && definition.Category != "overlay_v3_image" {
			return false
		}
	} else if definition.Category != "overlay_v3_image" {
		return false
	}
	// This track check is a geospatial invariant, not a second family allowlist:
	// it prevents an authored map motion from detaching the raster from its pins.
	if len(definition.Tracks) == 0 {
		return false
	}
	for _, track := range definition.Tracks {
		switch track.Property {
		case "opacity", "scale", "scale_x", "scale_y", "blur":
		default:
			return false
		}
	}
	return true
}
