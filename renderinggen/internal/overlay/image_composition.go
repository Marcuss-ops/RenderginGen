package overlay

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
		ID: "single_image", Zone: ZoneImages, MotionTarget: "image",
		Description: "Shows motions compatible with the single-image target.",
		Composition: RuntimeAnimationComposition{ImageCount: 1},
	},
	{
		ID: "image_double", Zone: ZoneImages, MotionTarget: "image",
		Description: "Each of the two image layers selects its own image motion and keeps its own timing.",
		Composition: RuntimeAnimationComposition{ImageCount: 2},
	},
	{
		ID: "image_triplet", Zone: ZoneImages, MotionTarget: "image",
		Description: "Each of the three image layers selects its own image motion and keeps its own timing.",
		Composition: RuntimeAnimationComposition{ImageCount: 3},
	},
	{
		ID: "image_four", Zone: ZoneImages, MotionTarget: "image",
		Description: "Each of the four image layers selects its own image motion and keeps its own timing.",
		Composition: RuntimeAnimationComposition{ImageCount: 4},
	},
	{
		ID: "image_five", Zone: ZoneImages, MotionTarget: "image",
		Description: "Each of the five image layers selects its own image motion and keeps its own timing.",
		Composition: RuntimeAnimationComposition{ImageCount: 5},
	},
	{
		ID: "single_image_with_text", Zone: ZoneImagesWithText, MotionTarget: "image",
		Description: "One image with its own caption; the image motion is the item motion and the caption motion is selected separately.",
		Composition: RuntimeAnimationComposition{ImageCount: 1, Caption: &RuntimeCaptionCount{Minimum: 1, Maximum: 1}},
	},
	{
		ID: "image_double_with_text", Zone: ZoneImagesWithText, MotionTarget: "image",
		Description: "Each of the two image layers selects its own image motion while every caption selects its own caption motion.",
		Composition: RuntimeAnimationComposition{ImageCount: 2, Caption: &RuntimeCaptionCount{Minimum: 1, Maximum: 2}},
	},
	{
		ID: "image_triplet_with_text", Zone: ZoneImagesWithText, MotionTarget: "image",
		Description: "Each of the three image layers selects its own image motion while every caption selects its own caption motion.",
		Composition: RuntimeAnimationComposition{ImageCount: 3, Caption: &RuntimeCaptionCount{Minimum: 1, Maximum: 3}},
	},
	{
		ID: "image_four_with_text", Zone: ZoneImagesWithText, MotionTarget: "image",
		Description: "Each of the four image layers selects its own image motion while every caption selects its own caption motion.",
		Composition: RuntimeAnimationComposition{ImageCount: 4, Caption: &RuntimeCaptionCount{Minimum: 1, Maximum: 4}},
	},
	{
		ID: "image_five_with_text", Zone: ZoneImagesWithText, MotionTarget: "image",
		Description: "Each of the five image layers selects its own image motion while every caption selects its own caption motion.",
		Composition: RuntimeAnimationComposition{ImageCount: 5, Caption: &RuntimeCaptionCount{Minimum: 1, Maximum: 5}},
	},
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
	return nil
}

func maxImageCompositionCount() int {
	maximum := 0
	for _, definition := range imageCompositionCatalog {
		if definition.Composition.ImageCount > maximum {
			maximum = definition.Composition.ImageCount
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
