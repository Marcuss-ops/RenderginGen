package motion

import (
	"math"
	"testing"
)

// The Visual Accents V1 acceptance suite: the four official families
// (brush_v1, web_rect_v1, paint_v1, light_leak_v1) with exactly 12 motions
// each, the V1 metadata contract, and family-specific recipe shapes.

func TestVisualAccentsV1_FamilyCounts(t *testing.T) {
	for _, category := range VisualAccentsV1Categories {
		ids := Registry.CategoryMotionIDs(category)
		if len(ids) != 12 {
			t.Fatalf("%s family has %d motions, want 12: %v", category, len(ids), ids)
		}
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if seen[id] {
				t.Errorf("%s repeats motion %q", category, id)
			}
			seen[id] = true
		}
	}
	if got := len(Registry.VisualAccentsV1FamilyMotionIDs()); got != 48 {
		t.Fatalf("visual accents V1 total = %d motions, want 48", got)
	}
}

func TestVisualAccentsV1_MetadataContract(t *testing.T) {
	for _, category := range VisualAccentsV1Categories {
		for _, id := range Registry.CategoryMotionIDs(category) {
			plugin, err := Registry.Resolve(id)
			if err != nil {
				t.Fatalf("resolve %q: %v", id, err)
			}
			declarative, ok := plugin.(DeclarativePlugin)
			if !ok {
				t.Fatalf("%s motion %q is not catalog-declarative", category, id)
			}
			definition := declarative.Definition
			if definition.Category != category {
				t.Errorf("motion %q declares category %q, want %q", id, definition.Category, category)
			}
			if definition.Unit != "layer" {
				t.Errorf("motion %q unit %q, want layer", id, definition.Unit)
			}
			if definition.RenderSafe == nil || !*definition.RenderSafe {
				t.Errorf("motion %q is not render_safe", id)
			}
			if definition.DurationBounds == nil || definition.DurationBounds.MinimumFrames < 1 {
				t.Errorf("motion %q has no valid duration bounds", id)
			}
			if definition.Enter <= 0 {
				t.Errorf("motion %q enter = %d, want positive", id, definition.Enter)
			}
			hasTarget := false
			for _, target := range definition.Targets {
				hasTarget = hasTarget || target == "image"
			}
			if !hasTarget {
				t.Errorf("motion %q does not target image", id)
			}
			has3D := false
			for _, track := range definition.Tracks {
				has3D = has3D || IsCameraBacked3DProperty(track.Property)
			}
			if definition.Requires3D != nil && *definition.Requires3D != has3D {
				t.Errorf("motion %q requires_3d=%v, tracks say %v", id, *definition.Requires3D, has3D)
			}
			if definition.RequiresCamera != nil && *definition.RequiresCamera != has3D {
				t.Errorf("motion %q requires_camera=%v, tracks say %v", id, *definition.RequiresCamera, has3D)
			}
		}
	}
}

func TestVisualAccentsV1_TracksAreFiniteAndResting(t *testing.T) {
	for _, category := range VisualAccentsV1Categories {
		for _, id := range Registry.CategoryMotionIDs(category) {
			plugin, _ := Registry.Resolve(id)
			definition := plugin.(DeclarativePlugin).Definition
			for _, track := range definition.Tracks {
				frames := make([]int64, 0, len(track.Keyframes))
				for _, keyframe := range track.Keyframes {
					frames = append(frames, keyframe.Frame)
					for _, value := range numericKeyframeValues(keyframe.Value) {
						if math.IsNaN(value) || math.IsInf(value, 0) {
							t.Errorf("%s motion %q track %s has non-finite value", category, id, track.Property)
						}
					}
				}
				if frames[0] != 0 {
					t.Errorf("%s motion %q track %s starts at frame %d, want 0", category, id, track.Property, frames[0])
				}
				last := track.Keyframes[len(track.Keyframes)-1]
				value, ok := last.Value.(float64)
				if !ok {
					continue
				}
				switch track.Property {
				case "scale", "scale_x", "scale_y", "opacity":
					if value != 1 {
						t.Errorf("%s motion %q %s rests at %v, want 1", category, id, track.Property, value)
					}
				case "position_x", "position_y", "position_z", "rotation_x", "rotation_y", "rotation_z":
					if value != 0 {
						t.Errorf("%s motion %q %s rests at %v, want 0", category, id, track.Property, value)
					}
				}
			}
		}
	}
}

func TestVisualAccentsV1_RecipeShapes(t *testing.T) {
	for _, category := range VisualAccentsV1Categories {
		for _, id := range Registry.CategoryMotionIDs(category) {
			plugin, _ := Registry.Resolve(id)
			definition := plugin.(DeclarativePlugin).Definition
			recipe := definition.ImageRecipe
			if recipe == nil {
				t.Fatalf("%s motion %q has no image recipe", category, id)
			}
			switch category {
			case "brush_v1":
				if len(recipe.Components) == 0 || recipe.Mask != nil {
					t.Fatalf("brush motion %q must lower through path components", id)
				}
				for _, component := range recipe.Components {
					if component.Shape != "path" || component.Stroke == nil || component.Trim == nil {
						t.Errorf("brush motion %q component %q is not a trimmed stroked path", id, component.ID)
					}
				}
			case "web_rect_v1":
				if len(recipe.Components) == 0 || recipe.Mask != nil {
					t.Fatalf("web rect motion %q must lower through panel components", id)
				}
				for _, component := range recipe.Components {
					if component.Shape != "rounded_rect" && component.Shape != "path" {
						t.Errorf("web rect motion %q component %q is not a panel shape", id, component.ID)
					}
				}
			case "paint_v1":
				if recipe.Mask == nil || len(recipe.Components) != 0 {
					t.Fatalf("paint motion %q must lower through a field mask", id)
				}
				if recipe.Mask.Kind != "field" || recipe.Mask.Field == nil {
					t.Errorf("paint motion %q mask is not a field mask", id)
				}
			case "light_leak_v1":
				if len(recipe.Components) == 0 || recipe.Mask != nil {
					t.Fatalf("light leak motion %q must lower through glow components", id)
				}
				for _, component := range recipe.Components {
					if component.Shape != "ellipse" {
						t.Errorf("light leak motion %q component %q is not an ellipse glow plate", id, component.ID)
					}
				}
			}
		}
	}
}

// The paint family's real acceptance gate: the lowered mask progress starts
// closed at 0 and ends open at 1, so the reveal covers nothing at its first
// frame and the full frame at its last.
func TestVisualAccentsV1_PaintProgressCoversZeroToFull(t *testing.T) {
	for _, id := range Registry.CategoryMotionIDs("paint_v1") {
		plugin, _ := Registry.Resolve(id)
		definition := plugin.(DeclarativePlugin).Definition
		progress := definition.ImageRecipe.Mask.Progress
		first, ok := motionNumericValue(progress.Keyframes[0].Value)
		if !ok || first != 0 {
			t.Errorf("paint motion %q mask progress starts at %v, want 0", id, progress.Keyframes[0].Value)
		}
		last, ok := motionNumericValue(progress.Keyframes[len(progress.Keyframes)-1].Value)
		if !ok || last != 1 {
			t.Errorf("paint motion %q mask progress ends at %v, want 1", id, progress.Keyframes[len(progress.Keyframes)-1].Value)
		}
	}
}

// The brush family's real acceptance gate: every stroke is trim-animated from
// closed to open, so a brush trait draws instead of popping.
func TestVisualAccentsV1_BrushStrokesTrimFromZeroToOne(t *testing.T) {
	trimVector := func(value any) []float64 {
		vector, ok := motionNumericVector(value)
		if !ok || len(vector) < 2 {
			return nil
		}
		return vector
	}
	for _, id := range Registry.CategoryMotionIDs("brush_v1") {
		plugin, _ := Registry.Resolve(id)
		definition := plugin.(DeclarativePlugin).Definition
		for _, component := range definition.ImageRecipe.Components {
			trim := component.Trim
			if trim == nil {
				continue
			}
			startKeys := trim.Animation.Keyframes
			first := trimVector(startKeys[0].Value)
			if first == nil || first[0] != 0 {
				t.Errorf("brush motion %q trim starts at %v, want [0, 0]", id, startKeys[0].Value)
			}
			last := trimVector(startKeys[len(startKeys)-1].Value)
			if last == nil || last[1] != 1 {
				t.Errorf("brush motion %q trim end rests at %v, want end 1", id, startKeys[len(startKeys)-1].Value)
			}
		}
	}
}
