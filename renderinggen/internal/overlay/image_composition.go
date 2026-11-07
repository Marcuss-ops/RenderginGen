package overlay

import "strings"

// imageCompositionDefinition is the canonical semantic composition record.
// The picker publishes it and the compiler validates composition_id against
// the same table.
type imageCompositionDefinition struct {
	ID           string
	Zone         string
	Description  string
	MotionTarget string
	Composition  RuntimeAnimationComposition
}

var imageCompositionCatalog = []imageCompositionDefinition{
	{
		ID: "single_image", Zone: ZoneImages, MotionTarget: TargetImage,
		Description: "Shows motions compatible with the single-image target.",
		Composition: RuntimeAnimationComposition{ImageCount: 1},
	},
	{
		ID: "multiple_images", Zone: ZoneImages, MotionTarget: TargetImageGroup,
		Description: "One animation is selected for the image group and reused for each image layer, regardless of group size.",
		Composition: RuntimeAnimationComposition{ImageCountRange: &RuntimeCountRange{Minimum: 2, Maximum: 5}},
	},
	{
		ID: "single_image_with_text", Zone: ZoneImagesWithText, MotionTarget: TargetImage,
		Description: "One image with its own caption; the image motion is the item motion and the caption motion is selected separately.",
		Composition: RuntimeAnimationComposition{ImageCount: 1, Caption: &RuntimeCaptionCount{Minimum: 1, Maximum: 1}},
	},
	{
		ID: "multiple_images_with_text", Zone: ZoneImagesWithText, MotionTarget: TargetImageGroup,
		Description: "One animation is selected for the image group and reused for each image layer; captions keep independent motion.",
		Composition: RuntimeAnimationComposition{ImageCountRange: &RuntimeCountRange{Minimum: 2, Maximum: 5}, Caption: &RuntimeCaptionCount{Minimum: 1, Maximum: 5}},
	},
}

// legacyImageCompositionAliases keep sealed jobs replayable while the picker
// publishes only count-independent multi-image choices.
var legacyImageCompositionAliases = map[string]int{
	"image_double": 2, "image_triplet": 3, "image_four": 4, "image_five": 5,
	"image_double_with_text": 2, "image_triplet_with_text": 3,
	"image_four_with_text": 4, "image_five_with_text": 5,
}

// captionTextLimitLines is the published dense-layout rule for a composition
// with the given number of captions. The denser the composition, the fewer lines
// a caption box may wrap to: three lines up to two captions, two lines up to
// four, one line beyond that. The picker and the compiler read the same rule
// instead of each guessing a separate limit.
func captionTextLimitLines(captionCount int) int {
	switch {
	case captionCount <= 2:
		return 3
	case captionCount <= 4:
		return 2
	default:
		return 1
	}
}

func imageCompositionFor(id string) *RuntimeAnimationComposition {
	for _, definition := range imageCompositionCatalog {
		if definition.ID == id {
			return cloneRuntimeAnimationComposition(definition.Composition)
		}
	}
	if count, ok := legacyImageCompositionAliases[id]; ok {
		composition := RuntimeAnimationComposition{ImageCount: count}
		if strings.HasSuffix(id, "_with_text") {
			composition.Caption = &RuntimeCaptionCount{Minimum: 1, Maximum: count}
		}
		return &composition
	}
	return nil
}

func maxImageCompositionCount() int {
	maximum := 0
	for _, definition := range imageCompositionCatalog {
		if definition.Composition.ImageCount > maximum {
			maximum = definition.Composition.ImageCount
		}
		if definition.Composition.ImageCountRange != nil && definition.Composition.ImageCountRange.Maximum > maximum {
			maximum = definition.Composition.ImageCountRange.Maximum
		}
	}
	return maximum
}

func cloneRuntimeAnimationComposition(composition RuntimeAnimationComposition) *RuntimeAnimationComposition {
	cloned := composition
	if composition.Caption != nil {
		caption := *composition.Caption
		cloned.Caption = &caption
	}
	return &cloned
}
