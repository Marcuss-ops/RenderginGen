package overlay

import (
	"fmt"
	"sort"
	"strings"
)

// resolveBackgroundFit validates the requested background fit.
//
// The vocabulary itself lives once in design_tokens.go (fitVocabulary), so the
// background and the item/video lowering cannot accept different spellings: a
// producer could previously ask for a treatment that one site accepted and the
// other silently rewrote — the historical "blur_cover" background hint did
// exactly that. Empty means the deterministic crop-to-fill default ("cover"),
// which fills the canvas at any source aspect ratio.
func resolveBackgroundFit(requested string) (string, error) {
	fit := strings.ToLower(strings.TrimSpace(requested))
	if fit == "" {
		return FitCover, nil
	}
	if !isSupportedFit(fit) {
		return "", fmt.Errorf("overlay: unsupported background fit %q (supported: %s)", requested, supportedFits)
	}
	return fit, nil
}

// unknownTemplates returns the sorted, de-duplicated template_ids of the items
// that found no registry row. It is the only place the fall-through is
// classified, and it reads the SAME resolved spec the compiler lowered, so it
// can never disagree with what was actually emitted.
func unknownTemplates(items []resolvedItem) []string {
	seen := make(map[string]bool)
	var out []string
	for _, ri := range items {
		if ri.Spec.Registered {
			continue
		}
		id := strings.TrimSpace(ri.Item.Template)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
