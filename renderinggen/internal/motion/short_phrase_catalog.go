package motion

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// C++ owns the short phrase recipes. This checked-in generated artifact is
// refreshed alongside chronontemplate_catalog.v1.json.
//
//go:embed catalog/short_phrase_motion.v1.json
var shortPhraseCatalogJSON []byte

type shortPhraseCatalog struct {
	Schema    string              `json:"schema"`
	Version   int                 `json:"version"`
	CatalogID string              `json:"catalog_id"`
	Recipes   []shortPhraseRecipe `json:"recipes"`
}

type shortPhraseSelector struct {
	Unit   string `json:"unit"`
	Order  string `json:"order"`
	Window string `json:"window"`
}

type shortPhraseAnimator struct {
	Selector   shortPhraseSelector `json:"selector"`
	Properties []TrackDefinition   `json:"properties"`
}

type shortPhraseRecipe struct {
	ID              string                `json:"id"`
	Family          string                `json:"family"`
	Subcategory     string                `json:"subcategory"`
	Targets         []string              `json:"targets"`
	Title           string                `json:"title"`
	Enter           int                   `json:"enter"`
	Exit            string                `json:"exit"`
	Tracks          []TrackDefinition     `json:"tracks"`
	Light           bool                  `json:"light"`
	WhiteBackground bool                  `json:"white_background"`
	Selector        shortPhraseSelector   `json:"selector"`
	TextAnimators   []shortPhraseAnimator `json:"text_animators"`
	Timing          struct {
		ExitFrames int `json:"out_frames"`
	} `json:"timing"`
}

// ShortPhraseStyle describes one C++-authored selectable animation.
type ShortPhraseStyle struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Subcategory string `json:"subcategory"`
}

func readShortPhraseStyles() ([]shortPhraseRecipe, error) {
	var catalog shortPhraseCatalog
	if err := json.Unmarshal(shortPhraseCatalogJSON, &catalog); err != nil {
		return nil, fmt.Errorf("motion: decode short phrase catalog: %w", err)
	}
	if catalog.Schema != "chronontemplate.short-phrase-motion.v1" || catalog.Version != 1 || catalog.CatalogID != "short_phrase_motion_v1" {
		return nil, fmt.Errorf("motion: unsupported short phrase catalog %q v%d", catalog.Schema, catalog.Version)
	}
	var recipes []shortPhraseRecipe
	seen := map[string]bool{}
	for _, recipe := range catalog.Recipes {
		if !containsMotionString(recipe.Targets, "short_phrase") {
			continue
		}
		if recipe.ID == "" || seen[recipe.ID] || recipe.Enter <= 0 {
			return nil, fmt.Errorf("motion: invalid or duplicate short phrase style %q", recipe.ID)
		}
		seen[recipe.ID] = true
		recipes = append(recipes, recipe)
	}
	for _, recipe := range recipes {
		if !containsMotionString(recipe.Targets, "short_phrase") {
			return nil, fmt.Errorf("motion: short phrase style %q must declare short_phrase target", recipe.ID)
		}
		if len(recipe.TextAnimators) > 0 && !containsMotionString(recipe.Targets, "caption") {
			return nil, fmt.Errorf("motion: short phrase style %q with text animators must declare caption target", recipe.ID)
		}
	}
	return recipes, nil
}

func shortPhraseMotionDefinition(recipe shortPhraseRecipe) MotionDefinition {
	definition := MotionDefinition{
		ID: recipe.ID, Category: "short_phrase_style", Targets: append([]string(nil), recipe.Targets...), Enter: recipe.Enter,
		Exit: recipe.Timing.ExitFrames, Tracks: recipe.Tracks,
		TextAnimators: make([]TextAnimatorDefinition, 0, len(recipe.TextAnimators)),
	}
	for index, animator := range recipe.TextAnimators {
		selector := shortPhraseSelectorDefinition(animator.Selector)
		properties := make([]TrackDefinition, len(animator.Properties))
		for propertyIndex, track := range animator.Properties {
			properties[propertyIndex] = lowerShortPhraseColorTrack(track, recipe.Light || recipe.WhiteBackground)
		}
		definition.TextAnimators = append(definition.TextAnimators, TextAnimatorDefinition{
			ID:         fmt.Sprintf("%s_animator_%d", recipe.ID, index),
			Selector:   selector,
			Properties: properties,
		})
	}
	return definition
}

// ChrononTemplate authors short-phrase color mixes as scalar fill_blue,
// fill_gray and fill_orange tracks. The renderer contract uses native animated
// RGBA fill_color tracks, so project the authoring shorthand at the catalog boundary.
func lowerShortPhraseColorTrack(track TrackDefinition, light bool) TrackDefinition {
	var target [4]float64
	switch track.Property {
	case "fill_blue":
		target = [4]float64{0.30, 0.55, 1, 1}
	case "fill_orange":
		target = [4]float64{1, 0.38, 0.12, 1}
	case "fill_gray":
		gray := 0.40
		if light {
			gray = 0.62
		}
		target = [4]float64{gray, gray, gray, 1}
	default:
		return track
	}
	rest := [4]float64{1, 1, 1, 1}
	if light {
		rest = [4]float64{0.03, 0.03, 0.03, 1}
	}
	converted := TrackDefinition{Property: "fill_color", Easing: track.Easing, Keyframes: make([]AnimationKeyframe, 0, len(track.Keyframes))}
	for _, key := range track.Keyframes {
		mix, ok := shortPhraseNumericValue(key.Value)
		if !ok {
			// Leave malformed authoring data untouched so ValidateDefinition
			// reports it with the motion ID instead of silently changing it.
			return track
		}
		if mix < 0 {
			mix = 0
		}
		if mix > 1 {
			mix = 1
		}
		color := make([]float64, 4)
		for channel := range color {
			color[channel] = rest[channel] + (target[channel]-rest[channel])*mix
		}
		converted.Keyframes = append(converted.Keyframes, AnimationKeyframe{Frame: key.Frame, Value: color})
	}
	return converted
}

func shortPhraseNumericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func shortPhraseSelectorDefinition(selector shortPhraseSelector) SelectorDefinition {
	definition := SelectorDefinition{Kind: selector.Unit, Shape: shortPhraseWindowShape(selector.Window), Order: selector.Order}
	if strings.HasPrefix(selector.Window, "pick:") {
		parts := strings.Split(selector.Window, ":")
		if len(parts) == 3 {
			index, indexErr := strconv.Atoi(parts[1])
			count, countErr := strconv.Atoi(parts[2])
			if indexErr == nil && countErr == nil && index >= 0 && count > index {
				start, end := float64(index)/float64(count), float64(index+1)/float64(count)
				definition.RangeStart, definition.RangeEnd = &start, &end
			}
		}
	} else if selector.Window == "reveal" || selector.Window == "reveal_soft" || selector.Window == "band" {
		definition.Stagger = 1
	}
	return definition
}

func shortPhraseWindowShape(window string) string {
	if strings.HasPrefix(window, "pick:") {
		return "square"
	}
	switch window {
	case "reveal_soft":
		return "smooth"
	case "band":
		return "triangle"
	default:
		return "square"
	}
}

// ShortPhraseStyleIDs returns the generated short-phrase style ids in C++ catalog order.
func (r *RegistryType) ShortPhraseStyleIDs() []string {
	recipes, err := readShortPhraseStyles()
	if err != nil {
		return nil
	}
	ids := make([]string, len(recipes))
	for i := range recipes {
		ids[i] = recipes[i].ID
	}
	return ids
}

// ShortPhraseStyles returns renderer-facing labels for the Short Phrases style picker.
func (r *RegistryType) ShortPhraseStyles() []ShortPhraseStyle {
	recipes, err := readShortPhraseStyles()
	if err != nil {
		return nil
	}
	styles := make([]ShortPhraseStyle, len(recipes))
	for i, recipe := range recipes {
		styles[i] = ShortPhraseStyle{ID: recipe.ID, Title: recipe.Title, Subcategory: recipe.Subcategory}
	}
	return styles
}

func registerShortPhraseStyles(registry *RegistryType) error {
	recipes, err := readShortPhraseStyles()
	if err != nil {
		return err
	}
	for _, recipe := range recipes {
		definition := shortPhraseMotionDefinition(recipe)
		if err := ValidateDefinition(definition); err != nil {
			return fmt.Errorf("motion: short phrase style %q: %w", recipe.ID, err)
		}
		if err := registry.Register(definition.ID, DeclarativePlugin{Definition: definition}); err != nil {
			return err
		}
	}
	return nil
}
