package motion

import "testing"

func TestTypewriterVariantsHaveVisibleNonConstantMotion(t *testing.T) {
	catalog, err := Canonical()
	if err != nil {
		t.Fatalf("canonical catalog: %v", err)
	}
	want := map[string]bool{
		"typewriter_clean":    true,
		"typewriter_pop":      true,
		"typewriter_neon":     true,
		"typewriter_tracking": true,
		"typewriter_glitch":   true,
	}
	found := make(map[string]bool, len(want))
	for _, definition := range catalog.Motions {
		if !want[definition.ID] {
			continue
		}
		found[definition.ID] = true
		if definition.Enter != 42 {
			t.Errorf("%s: enter=%d, want 42-frame reveal", definition.ID, definition.Enter)
		}
		if len(definition.TextAnimators) == 0 {
			t.Errorf("%s: missing text animator", definition.ID)
			continue
		}
		properties := definition.TextAnimators[0].Properties
		var opacityEnd float64
		visible := false
		var varyingMotion bool
		number := func(value any) float64 {
			parsed, ok := value.(float64)
			if !ok {
				t.Fatalf("%s: keyframe value %T is not numeric", definition.ID, value)
			}
			return parsed
		}
		for _, property := range properties {
			if len(property.Keyframes) < 2 {
				continue
			}
			first := number(property.Keyframes[0].Value)
			last := number(property.Keyframes[len(property.Keyframes)-1].Value)
			if property.Property == "opacity" {
				opacityEnd = last
				visible = first < last && last > 0
			}
			if property.Property != "opacity" && first != last {
				varyingMotion = true
			}
		}
		if !visible {
			t.Errorf("%s: opacity must reveal from 0 to a positive value", definition.ID)
		}
		if definition.ID != "typewriter_clean" && !varyingMotion {
			t.Errorf("%s: variant has no non-constant motion property", definition.ID)
		}
		if opacityEnd <= 0 {
			t.Errorf("%s: final opacity=%v, want > 0", definition.ID, opacityEnd)
		}
	}
	for id := range want {
		if !found[id] {
			t.Errorf("catalog is missing %s", id)
		}
	}
}
