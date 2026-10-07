package overlay

import "testing"

// The registry replaces the removed index groups: every composition class must
// be reachable as a derived-tag query with the historic membership counts.
func TestEntityStyleRegistryTagQueries(t *testing.T) {
	cases := map[string]int{
		"below":      20,
		"badge":      5,
		"camera":     5,
		"side":       5,
		"typewriter": 5,
	}
	for tag, want := range cases {
		got := entityStyleRegistry.query(entityStyleQuery{tag})
		if len(got) != want {
			t.Fatalf("tag %q matched %d styles, want %d", tag, len(got), want)
		}
		for _, idx := range got {
			style := premiumEntityStyles[idx]
			switch tag {
			case "below":
				if style.CaptionLayout != "below" && style.ImageSide != "center" {
					t.Fatalf("tag below matched style %q with layout %q side %q", style.ID, style.CaptionLayout, style.ImageSide)
				}
			case "badge":
				if !style.IsBadge {
					t.Fatalf("tag badge matched non-badge style %q", style.ID)
				}
			case "camera":
				if style.CameraMotionID == "" {
					t.Fatalf("tag camera matched style %q without camera motion", style.ID)
				}
			case "side":
				if style.CaptionLayout != "left" && style.CaptionLayout != "right" {
					t.Fatalf("tag side matched style %q with caption layout %q", style.ID, style.CaptionLayout)
				}
			case "typewriter":
				id := style.CaptionMotionID
				if len(id) < len(typewriterCaptionPrefix) || id[:len(typewriterCaptionPrefix)] != typewriterCaptionPrefix {
					t.Fatalf("tag typewriter matched style %q with caption motion %q", style.ID, id)
				}
			}
		}
	}
	if got := entityStyleRegistry.query(nil); len(got) != len(premiumEntityStyles) {
		t.Fatalf("empty query matched %d styles, want %d", len(got), len(premiumEntityStyles))
	}
	if got := entityStyleRegistry.query(entityStyleQuery{"side", "camera"}); len(got) != 0 {
		t.Fatalf("impossible side+camera query matched %v, want no styles", got)
	}
	if got := entityStyleRegistry.query(entityStyleQuery{"unknown-tag"}); len(got) != 0 {
		t.Fatalf("unknown tag query matched %v, want no styles", got)
	}
}

// Tag selectors and their _random aliases are one registry query with one
// sampling salt, and sampling is deterministic for the identity tuple.
func TestEntityStyleRegistrySelectorsSampleDeterministically(t *testing.T) {
	planID, videoID, itemID := "plan-reg", "video-reg", "item-reg"
	for _, selector := range []string{"", "random", "premium_random_v1", "testo_sotto", "below", "premium_below_random_v1", "caption_below", "badge", "badge_random", "camera", "camera_random", "side", "side_random", "typewriter", "typewriter_random"} {
		first, ok := ResolveEntityStyle(selector, planID, videoID, itemID)
		if !ok {
			t.Fatalf("selector %q did not resolve", selector)
		}
		second, _ := ResolveEntityStyle(selector, planID, videoID, itemID)
		if first.ID != second.ID {
			t.Fatalf("selector %q is not deterministic: %q then %q", selector, first.ID, second.ID)
		}
	}
	if alias, _ := ResolveEntityStyle("badge_random", planID, videoID, itemID); alias.ID == "" {
		t.Fatal("badge_random resolved empty")
	}
	base, _ := ResolveEntityStyle("badge", planID, videoID, itemID)
	alias, _ := ResolveEntityStyle("badge_random", planID, videoID, itemID)
	if base.ID != alias.ID {
		t.Fatalf("badge aliases sample differently: %q vs %q", base.ID, alias.ID)
	}
	below, _ := ResolveEntityStyle("testo_sotto", planID, videoID, itemID)
	if below.CaptionLayout != "below" && below.ImageSide != "center" {
		t.Fatalf("testo_sotto sampled %q outside the below class", below.ID)
	}
}

// Every catalog style must remain individually resolvable through the same
// registry path the producers use.
func TestEntityStyleRegistryLookupCoversAllStyles(t *testing.T) {
	for _, style := range premiumEntityStyles {
		got, ok := ResolveEntityStyle(style.ID, "p", "v", "i")
		if !ok {
			t.Fatalf("style %q no longer resolves", style.ID)
		}
		want := applyBadgeRandomizationForTest(style, "p", "v", "i")
		if got.BadgeColor != want.BadgeColor || got.CaptionColor != want.CaptionColor {
			t.Fatalf("style %q badge colors drifted: %+v vs %+v", style.ID, got, want)
		}
	}
	for alias, wantID := range entityStyleAliases {
		got, ok := ResolveEntityStyle(alias, "p", "v", "i")
		if !ok || got.ID != wantID {
			t.Fatalf("alias %q resolved to %+v, %v; want style %q", alias, got, ok, wantID)
		}
	}
	if _, ok := ResolveEntityStyle("01", "p", "v", "i"); !ok {
		t.Fatal("legacy reference lookup lost")
	}
	if _, ok := ResolveEntityStyle("1", "p", "v", "i"); !ok {
		t.Fatal("unpadded legacy reference lookup lost")
	}
}

func applyBadgeRandomizationForTest(style entityStyleVariant, planID, videoID, itemID string) entityStyleVariant {
	applyBadgeRuntimeRandomization(&style, style.ID, planID, videoID, itemID)
	return style
}
