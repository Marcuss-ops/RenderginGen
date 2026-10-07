package overlay

import (
	"strconv"
	"strings"
)

func imageCompositionCardinality(composition RuntimeAnimationComposition) string {
	cardinality := countedLabel(composition.ImageCount, "image", "images")
	if composition.Caption != nil {
		cardinality += " with text"
	}
	return cardinality
}

func mapCompositionCardinality(mapCount int) string {
	return countedLabel(mapCount, "map", "maps")
}

func countedLabel(count int, singular, plural string) string {
	word := strconv.Itoa(count)
	switch count {
	case 1:
		word = "one"
	case 2:
		word = "two"
	case 3:
		word = "three"
	case 4:
		word = "four"
	case 5:
		word = "five"
	}
	noun := plural
	if count == 1 {
		noun = singular
	}
	return word + " " + noun
}

func compositionDescription(cardinality, detail string) string {
	label := strings.ToUpper(cardinality[:1]) + cardinality[1:]
	if detail == "" {
		return label + "."
	}
	return label + ". " + detail
}
