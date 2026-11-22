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
