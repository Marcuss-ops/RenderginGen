package motion

import (
	"fmt"
	"math"
	"testing"
)

// The Editorial Visual Motion V1 acceptance suite. Every assertion here is a
// milestone gate from the goal checklist: exact final resting poses, camera
// capability metadata, deterministic selectors, and catalog parity between
// the authored tool and the embedded artifact.

func loadV1Family(t *testing.T, category string, want int) []MotionDefinition {
	t.Helper()
	ids := Registry.CategoryMotionIDs(category)
	if len(ids) != want {
		t.Fatalf("%s family has %d motions, want %d: %v", category, len(ids), want, ids)
	}
	definitions := make([]MotionDefinition, 0, len(ids))
	for _, id := range ids {
		plugin, err := Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %s motion %q: %v", category, id, err)
		}
		declarative, ok := plugin.(DeclarativePlugin)
		if !ok {
			t.Fatalf("%s motion %q is not a declarative catalog motion", category, id)
		}
		definitions = append(definitions, declarative.Definition)
	}
	return definitions
}

func assertFiniteAndResting(t *testing.T, category string, definitions []MotionDefinition) {
	t.Helper()
	for _, d := range definitions {
		for _, track := range d.Tracks {
			for _, keyframe := range track.Keyframes {
				for _, value := range numericKeyframeValues(keyframe.Value) {
					if math.IsNaN(value) || math.IsInf(value, 0) {
						t.Fatalf("%s motion %q: track %s has a non-finite value %v", category, d.ID, track.Property, value)
					}
				}
			}
			last := track.Keyframes[len(track.Keyframes)-1]
			value, ok := last.Value.(float64)
			if !ok {
				t.Fatalf("%s motion %q: track %s resting value has type %T", category, d.ID, track.Property, last.Value)
			}
			switch track.Property {
			case "scale", "scale_x", "scale_y", "opacity":
				if value != 1 {
					t.Fatalf("%s motion %q: %s rests at %v, want exactly 1", category, d.ID, track.Property, value)
				}
			case "position_x", "position_y", "position_z", "rotation_x", "rotation_y", "rotation_z":
				if value != 0 {
					t.Fatalf("%s motion %q: %s rests at %v, want exactly 0", category, d.ID, track.Property, value)
				}
			}
		}
		for _, animator := range d.TextAnimators {
			for _, track := range animator.Properties {
				last := track.Keyframes[len(track.Keyframes)-1]
				if value, ok := last.Value.(float64); ok {
					switch track.Property {
					case "scale", "scale_x", "scale_y", "opacity":
						if value != 1 {
							t.Fatalf("%s motion %q: animator %s rests at %v, want exactly 1", category, d.ID, track.Property, value)
						}
					case "position_x", "position_y":
						if value != 0 {
							t.Fatalf("%s motion %q: animator %s rests at %v, want exactly 0", category, d.ID, track.Property, value)
						}
					}
				}
			}
		}
	}
}

func numericKeyframeValues(value any) []float64 {
	switch v := value.(type) {
	case float64:
		return []float64{v}
	case []float64:
		return v
	case []any:
		out := make([]float64, 0, len(v))
		for _, item := range v {
			if f, ok := item.(float64); ok {
				out = append(out, f)
			}
		}
		return out
	default:
		return nil
	}
}

func TestEditorialImageV1FamilyCount(t *testing.T) {
	loadV1Family(t, "editorial_image_v1", 14)
}

func TestEditorialImageV1FinalPose(t *testing.T) {
	definitions := loadV1Family(t, "editorial_image_v1", 14)
	assertFiniteAndResting(t, "editorial_image_v1", definitions)
}

func TestEditorialImageV1NoNaN(t *testing.T) {
	// Covered with the final pose walk, but kept as an explicit named gate:
	// a NaN keyframe poisons the whole render plan downstream.
	definitions := loadV1Family(t, "editorial_image_v1", 14)
	for _, d := range definitions {
		for _, track := range d.Tracks {
			for _, keyframe := range track.Keyframes {
				for _, value := range numericKeyframeValues(keyframe.Value) {
					if math.IsNaN(value) || math.IsInf(value, 0) {
						t.Fatalf("editorial_image_v1 motion %q has non-finite %s value %v", d.ID, track.Property, value)
					}
				}
			}
		}
	}
}

func TestEditorialImageV1CameraCompatibility(t *testing.T) {
	definitions := loadV1Family(t, "editorial_image_v1", 14)
	for _, d := range definitions {
		has3D := false
		for _, track := range d.Tracks {
			has3D = has3D || IsCameraBacked3DProperty(track.Property)
		}
		if d.Requires3D != nil && *d.Requires3D != has3D {
			t.Fatalf("editorial_image_v1 motion %q: requires_3d=%v but tracks say %v", d.ID, *d.Requires3D, has3D)
		}
		// Every camera-backed property must route through Chronon's 3D path —
		// the registry derives enable_3d from exactly these properties.
		if has3D && d.RenderSafe == nil {
			t.Fatalf("editorial_image_v1 motion %q: 3D motion without render_safe metadata", d.ID)
		}
	}
}

func TestEditorialImageV1DurationBounds(t *testing.T) {
	definitions := loadV1Family(t, "editorial_image_v1", 14)
	for _, d := range definitions {
		if d.DurationBounds == nil || d.DurationBounds.MinimumFrames < 1 || d.DurationBounds.MaximumFrames < d.DurationBounds.MinimumFrames {
			t.Fatalf("editorial_image_v1 motion %q has invalid duration bounds: %+v", d.ID, d.DurationBounds)
		}
		if d.Enter <= 0 || int64(d.Enter) < d.DurationBounds.MinimumFrames {
			t.Fatalf("editorial_image_v1 motion %q: enter %d outside its duration bounds", d.ID, d.Enter)
		}
	}
}

func TestEditorialImageV1CatalogParity(t *testing.T) {
	// The vocabulary is owned by the authoring tool; the embedded artifact
	// must match it exactly or the two disagree about what V1 means.
	catalog, err := Canonical()
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]MotionDefinition, len(catalog.Motions))
	for _, definition := range catalog.Motions {
		byID[definition.ID] = definition
	}
	for _, id := range []string{
		"image_collage_scatter", "image_card_flip", "image_depth_cascade",
		"image_depth_dolly", "image_document_push", "image_evidence_focus",
		"image_float_settle", "image_focus_push", "image_orbit_enter",
		"image_perspective_stack", "image_photo_drop", "image_roll_in",
		"image_tilt_parallax", "image_yaw_reveal",
	} {
		definition, ok := byID[id]
		if !ok {
			t.Errorf("editorial_image_v1 motion %q missing from canonical catalog", id)
			continue
		}
		if definition.Category != "editorial_image_v1" {
			t.Errorf("motion %q carries category %q, want editorial_image_v1", id, definition.Category)
		}
	}
}

func TestText3DFamilyCount(t *testing.T) {
	loadV1Family(t, "text_3d_v1", 8)
}

func TestText3DMotionsRequire3D(t *testing.T) {
	definitions := loadV1Family(t, "text_3d_v1", 8)
	for _, d := range definitions {
		has3D := false
		for _, track := range d.Tracks {
			has3D = has3D || IsCameraBacked3DProperty(track.Property)
		}
		for _, animator := range d.TextAnimators {
			for _, track := range animator.Properties {
				has3D = has3D || IsCameraBacked3DProperty(track.Property)
			}
		}
		if !has3D {
			t.Fatalf("text_3d_v1 motion %q carries no camera-backed 3D motion", d.ID)
		}
		if d.Requires3D == nil || !*d.Requires3D {
			t.Fatalf("text_3d_v1 motion %q does not declare requires_3d", d.ID)
		}
	}
}

func TestText3DUsesKnownProperties(t *testing.T) {
	// Chronon's render-plan contract is the authority: layer tracks may
	// animate XYZ and rotations, animator tracks are limited to the
	// in-plane properties the glyph pipeline supports.
	definitions := loadV1Family(t, "text_3d_v1", 8)
	for _, d := range definitions {
		for _, track := range d.Tracks {
			if !layerProperties[track.Property] {
				t.Errorf("text_3d_v1 motion %q: layer track %q is outside the renderer vocabulary", d.ID, track.Property)
			}
		}
		for _, animator := range d.TextAnimators {
			for _, track := range animator.Properties {
				if !animatorProperties[track.Property] {
					t.Errorf("text_3d_v1 motion %q: animator property %q is outside the renderer vocabulary", d.ID, track.Property)
				}
			}
		}
	}
}

func TestText3DFinalPoseRestored(t *testing.T) {
	definitions := loadV1Family(t, "text_3d_v1", 8)
	assertFiniteAndResting(t, "text_3d_v1", definitions)
}

func TestText3DWordSelector(t *testing.T) {
	definition := v1Definition(t, "text_3d_v1", "text_3d_word_cascade")
	if len(definition.TextAnimators) == 0 {
		t.Fatal("text_3d_word_cascade has no text animator")
	}
	selector := definition.TextAnimators[0].Selector
	if selector.Kind != "word" {
		t.Fatalf("text_3d_word_cascade selector kind = %q, want word", selector.Kind)
	}
	if selector.Stagger <= 0 {
		t.Fatalf("text_3d_word_cascade stagger = %d, want a positive deterministic stagger", selector.Stagger)
	}
}

func TestText3DGlyphSelector(t *testing.T) {
	definition := v1Definition(t, "text_3d_v1", "text_3d_character_wave")
	if len(definition.TextAnimators) == 0 {
		t.Fatal("text_3d_character_wave has no text animator")
	}
	selector := definition.TextAnimators[0].Selector
	if selector.Kind != "glyph" {
		t.Fatalf("text_3d_character_wave selector kind = %q, want glyph", selector.Kind)
	}
}

func TestText3DDeterministicOrder(t *testing.T) {
	// Stagger must be deterministic: no random selector order anywhere in
	// the family, and every stagger is a fixed frame count.
	definitions := loadV1Family(t, "text_3d_v1", 8)
	for _, d := range definitions {
		for _, animator := range d.TextAnimators {
			if animator.Selector.Order == "random" {
				t.Fatalf("text_3d_v1 motion %q uses a random selector order", d.ID)
			}
			if animator.Selector.Stagger < 0 {
				t.Fatalf("text_3d_v1 motion %q has a negative stagger", d.ID)
			}
		}
	}
}

func TestText3DCatalogParity(t *testing.T) {
	definitions := loadV1Family(t, "text_3d_v1", 8)
	want := map[string]bool{
		"text_3d_yaw_flip_in": false, "text_3d_depth_push": false,
		"text_3d_tilt_rise": false, "text_3d_perspective_drop": false,
		"text_3d_roll_depth": false, "text_3d_camera_push": false,
		"text_3d_word_cascade": false, "text_3d_character_wave": false,
	}
	for _, d := range definitions {
		if _, ok := want[d.ID]; !ok {
			t.Errorf("unexpected text_3d_v1 motion %q", d.ID)
			continue
		}
		want[d.ID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("text_3d_v1 motion %q missing", id)
		}
	}
}

func TestEntityCaptionV1FamilyServesTextTargets(t *testing.T) {
	definitions := loadV1Family(t, "entity_caption_v1", 6)
	for _, d := range definitions {
		servesText := false
		for _, target := range d.Targets {
			servesText = servesText || target == "text" || target == "phrase"
		}
		if !servesText {
			t.Fatalf("entity_caption_v1 motion %q does not target text: %v", d.ID, d.Targets)
		}
	}
}

func TestWebV1CursorAndSpotlightAreDistinct(t *testing.T) {
	cursor := v1Definition(t, "web", "web_cursor_focus")
	spotlight := v1Definition(t, "web", "web_section_spotlight")
	if len(cursor.Tracks) == 0 || len(spotlight.Tracks) == 0 {
		t.Fatal("web V1 motions lowered without tracks")
	}
	cursorFingerprint := fmt.Sprintf("%v", cursor.Tracks)
	spotlightFingerprint := fmt.Sprintf("%v", spotlight.Tracks)
	if cursorFingerprint == spotlightFingerprint {
		t.Fatal("web_cursor_focus and web_section_spotlight share a track recipe")
	}
	for _, d := range []MotionDefinition{cursor, spotlight} {
		if d.RenderSafe == nil || !*d.RenderSafe || d.Requires3D == nil || !*d.Requires3D || d.RequiresCamera == nil || !*d.RequiresCamera {
			t.Fatalf("web motion %q lacks the full 3D metadata set", d.ID)
		}
	}
}

func v1Definition(t *testing.T, category, id string) MotionDefinition {
	t.Helper()
	plugin, err := Registry.Resolve(id)
	if err != nil {
		t.Fatalf("resolve %q: %v", id, err)
	}
	declarative, ok := plugin.(DeclarativePlugin)
	if !ok {
		t.Fatalf("motion %q is not declarative", id)
	}
	if declarative.Definition.Category != category {
		t.Fatalf("motion %q carries category %q, want %q", id, declarative.Definition.Category, category)
	}
	return declarative.Definition
}
