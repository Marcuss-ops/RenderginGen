package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// A pool entry is only useful if both runtime registries expose it and the
// lowering step produces actual tracks. This pins all three links for the web
// phrase family, including the long-lane lowering path.
func TestWebPhrasePoolReachesBothRuntimesAndLowers(t *testing.T) {
	pool := motion.PhraseMotionPool()
	var web []string
	for _, id := range pool {
		if strings.HasPrefix(id, "short_phrase_web_") {
			web = append(web, id)
		}
	}
	if len(web) != 30 {
		t.Fatalf("phrase_motion_pool exposes %d web gestures, want 30", len(web))
	}

	// The same text-only entrance must be offered by both phrase pickers. The
	// flat pool alone is not enough: a picker that filters by targets would
	// otherwise hide every web recipe from the long lane.
	shortIDs := useCaseMotionIDs(t, "short_important_phrase")
	longIDs := useCaseMotionIDs(t, "important_phrase")
	for _, id := range web {
		if !containsString(shortIDs, id) {
			t.Fatalf("short_important_phrase omits web gesture %q", id)
		}
		if !containsString(longIDs, id) {
			t.Fatalf("important_phrase omits web gesture %q", id)
		}
	}

	shortWeb := 0
	for _, id := range motion.Registry.ShortPhraseStyleIDs() {
		if strings.HasPrefix(id, "short_phrase_web_") {
			shortWeb++
		}
	}
	if shortWeb != 30 {
		t.Fatalf("short-phrase runtime registry exposes %d web gestures, want 30", shortWeb)
	}

	for _, id := range web {
		if !motion.Registry.Selectable(id) {
			t.Fatalf("web gesture %q is not selectable", id)
		}
		animation, err := lowerMotionForTarget(id, 90, 15, nil, "Web phrase runtime", 210, true, "web-test", "short_phrase")
		if err != nil {
			t.Fatalf("lower %q for short phrase: %v", id, err)
		}
		if animation == nil || (len(animation.Tracks) == 0 && len(animation.TextAnimators) == 0) {
			t.Fatalf("web gesture %q lowered to no animation", id)
		}
		if _, err := lowerMotionForTarget(id, 90, 15, nil, "Web phrase runtime", 210, true, "web-test", "important_phrase"); err != nil {
			t.Fatalf("lower %q for long phrase: %v", id, err)
		}
	}
}
