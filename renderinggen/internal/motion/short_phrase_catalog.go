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
	ID            string                `json:"id"`
	Family        string                `json:"family"`
	Subcategory   string                `json:"subcategory"`
	Title         string                `json:"title"`
	Enter         int                   `json:"enter"`
	Exit          string                `json:"exit"`
	Tracks        []TrackDefinition     `json:"tracks"`
	Selector      shortPhraseSelector   `json:"selector"`
	TextAnimators []shortPhraseAnimator `json:"text_animators"`
	Timing        struct {
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
		if recipe.Family != "product_video" {
			continue
		}
		if recipe.ID == "" || seen[recipe.ID] || recipe.Enter <= 0 {
			return nil, fmt.Errorf("motion: invalid or duplicate short phrase style %q", recipe.ID)
		}
		seen[recipe.ID] = true
		recipes = append(recipes, recipe)
	}
	if len(recipes) != 14 {
		return nil, fmt.Errorf("motion: short phrase catalog has %d product styles, expected 14", len(recipes))
	}
	return recipes, nil
}

func shortPhraseMotionDefinition(recipe shortPhraseRecipe) MotionDefinition {
	definition := MotionDefinition{
		ID: recipe.ID, Category: "short_phrase_style", Enter: recipe.Enter,
		Exit: recipe.Timing.ExitFrames, Tracks: recipe.Tracks,
		TextAnimators: make([]TextAnimatorDefinition, 0, len(recipe.TextAnimators)),
	}
	for index, animator := range recipe.TextAnimators {
		selector := shortPhraseSelectorDefinition(animator.Selector)
		definition.TextAnimators = append(definition.TextAnimators, TextAnimatorDefinition{
			ID:         fmt.Sprintf("%s_animator_%d", recipe.ID, index),
			Selector:   selector,
			Properties: animator.Properties,
		})
	}
	return definition
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

// ShortPhraseStyleIDs returns the generated product-video style ids in C++ catalog order.
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
