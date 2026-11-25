package overlay

import (
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// The typewriter family exists in two halves, and the split is the whole point:
// every short gesture must not appear in the long/important-phrase picker and
// every long one must not appear in the short picker. The pool is a flat list,
// so nothing but `targets` keeps them apart — an entry tagged `short_phrase`
// is grouped into the short use case alone, and an untagged phrase motion into
// the long one. Both the 2D and the 3D gestures are pinned here: the pack
// renders fifteen 2D and fourteen 3D clips, and all of them must survive the
// catalog round trip into the two pickers.
var (
	typewriterShortIDs = []string{
		"typewriter_glyph_drift_short",
		"typewriter_glyph_square_short",
		"typewriter_impact_push_short",
		"typewriter_rapid_fire_short",
		"typewriter_slow_type_short",
		"typewriter_soft_glide_short",
		"typewriter_staccato_short",
		"typewriter_word_burst_short",
		"typewriter_word_lift_short",
		"typewriter_word_step_short",
		"typewriter3d_yaw_flip_short",
		"typewriter3d_pitch_lift_short",
		"typewriter3d_roll_settle_short",
		"typewriter3d_z_bounce_short",
		"typewriter3d_orbit_short",
		"typewriter3d_hero_pull_short",
		"typewriter3d_parallax_split_short",
		"typewriter3d_camera_whip_short",
	}
	typewriterLongIDs = []string{
		"typewriter_breath_long",
		"typewriter_editorial_line_long",
		"typewriter_glyph_wrap_long",
		"typewriter_soft_edge_long",
		"typewriter_terminal_block_long",
		"typewriter3d_depth_push_long",
		"typewriter3d_card_swing_long",
		"typewriter3d_dolly_settle_long",
		"typewriter3d_spin_in_long",
		"typewriter3d_camera_rise_long",
		"typewriter3d_iso_slide_long",
	}
)

func useCaseMotionIDs(t *testing.T, id string) []string {
	t.Helper()
	for _, useCase := range CompiledRuntimeAnimationCatalog().UseCases {
		if useCase.ID == id {
			return useCase.MotionIDs
		}
	}
	t.Fatalf("runtime animation catalog has no %q use case", id)
	return nil
}

func typewriterIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		if strings.HasPrefix(id, "typewriter_") || strings.HasPrefix(id, "typewriter3d_") {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func assertSameIDs(t *testing.T, got, want []string) {
	t.Helper()
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if strings.Join(got, ",") != strings.Join(sorted, ",") {
		t.Fatalf("motion ids = %v, want %v", got, sorted)
	}
}

func TestTypewriterMotionSplitByPhraseLength(t *testing.T) {
	short := typewriterIDs(useCaseMotionIDs(t, "short_important_phrase"))
	long := typewriterIDs(useCaseMotionIDs(t, "important_phrase"))
	assertSameIDs(t, short, typewriterShortIDs)
	assertSameIDs(t, long, typewriterLongIDs)

	// A phrase motion carries no typography: the runtime picks font, size and
	// colour, so a pool entry that set them would override the job's style.
	for _, id := range append(append([]string{}, short...), long...) {
		definition, err := resolveMotionDefinition(id)
		if err != nil || definition == nil {
			t.Fatalf("%s: not resolvable from the catalog: %v", id, err)
		}
		if len(definition.LayerComponents) != 0 || definition.ImageRecipe != nil {
			t.Fatalf("%s: carries layer components; the pool entry must be animation only", id)
		}
		if definition.Unit == "" {
			t.Fatalf("%s: has no selector unit", id)
		}
		if len(definition.TextAnimators) == 0 {
			t.Fatalf("%s: animates nothing", id)
		}
		for _, animator := range definition.TextAnimators {
			if animator.Selector.Kind != definition.Unit {
				t.Fatalf("%s/%s: selector kind %q disagrees with the motion unit %q",
					id, animator.ID, animator.Selector.Kind, definition.Unit)
			}
			if animator.Selector.Stagger == 0 {
				t.Fatalf("%s/%s: no stagger, so the phrase would appear at once", id, animator.ID)
			}
			if len(animator.Properties) == 0 {
				t.Fatalf("%s/%s: no properties", id, animator.ID)
			}
			for _, property := range animator.Properties {
				switch property.Property {
				case "opacity", "position", "position_x", "position_y", "scale", "scale_x", "scale_y", "tracking":
				default:
					t.Fatalf("%s/%s: property %q is outside the GPU text-animator set",
						id, animator.ID, property.Property)
				}
			}
		}
	}
}

func TestTypewriterMotionsAreInThePhrasePool(t *testing.T) {
	pool := motion.PhraseMotionPool()
	for _, id := range append(append([]string{}, typewriterShortIDs...), typewriterLongIDs...) {
		if !containsString(pool, id) {
			t.Fatalf("phrase_motion_pool does not offer %q", id)
		}
	}
}
