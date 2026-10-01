package motion

import (
	"math"
	"testing"
)

func TestPresentationCatalogFamilyAndPresetParity(t *testing.T) {
	wantFamilies := []string{"date_v1", "entity_card_v1", "metric_v1"}
	gotFamilies := Registry.PresentationFamilyIDs()
	if len(gotFamilies) != len(wantFamilies) {
		t.Fatalf("families=%v want %v", gotFamilies, wantFamilies)
	}
	for i, want := range wantFamilies {
		if gotFamilies[i] != want {
			t.Fatalf("family[%d]=%q want %q", i, gotFamilies[i], want)
		}
	}
	for family, count := range map[string]int{"metric_v1": 10, "date_v1": 10, "entity_card_v1": 10} {
		ids := Registry.PresentationMotionIDs(family)
		if len(ids) != count {
			t.Fatalf("%s has %d presets, want %d", family, len(ids), count)
		}
		for _, id := range ids {
			plugin, err := Registry.Resolve(id)
			if err != nil {
				t.Fatalf("resolve %s: %v", id, err)
			}
			d := plugin.(DeclarativePlugin).Definition
			if d.Category != family || d.SupportedTemplate == "" || d.DurationBounds == nil || d.RenderSafe == nil || !*d.RenderSafe {
				t.Errorf("%s metadata incomplete: %+v", id, d)
			}
			if len(d.RequiredProperties) != len(d.Tracks) {
				t.Errorf("%s required properties do not enumerate every track", id)
			}
			for _, track := range d.Tracks {
				if len(track.Keyframes) < 2 || track.Keyframes[0].Frame != 0 {
					t.Errorf("%s/%s missing start/mid/end path", id, track.Property)
				}
				for _, key := range track.Keyframes {
					value, ok := motionNumericValue(key.Value)
					if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
						t.Errorf("%s/%s nonfinite keyframe", id, track.Property)
					}
				}
				last := track.Keyframes[len(track.Keyframes)-1]
				value, _ := motionNumericValue(last.Value)
				switch track.Property {
				case "opacity", "scale", "scale_x", "scale_y":
					if value != 1 {
						t.Errorf("%s/%s final pose=%v", id, track.Property, value)
					}
				case "position_x", "position_y", "position_z", "rotation_x", "rotation_y":
					if value != 0 {
						t.Errorf("%s/%s final pose=%v", id, track.Property, value)
					}
				}
			}
		}
	}
	if _, err := Registry.Resolve("metric_unknown_preset"); err == nil {
		t.Fatal("unknown presentation preset did not fail closed")
	}
	if got := Registry.PresentationMotionIDs("unknown_family"); len(got) != 0 {
		t.Fatalf("unknown family unexpectedly returned %v", got)
	}
}
