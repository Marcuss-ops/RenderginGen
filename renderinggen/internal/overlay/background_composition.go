package overlay

type backgroundCompositionDefinition struct {
	ID          string
	Zone        string
	Kind        string
	Description string
}

var backgroundCompositionCatalog = []backgroundCompositionDefinition{
	{
		ID: "background_color", Zone: ZoneBackground, Kind: "color",
		Description: "RGBA solid-color canvas background.",
	},
	{
		ID: "background_image", Zone: ZoneBackground, Kind: "image",
		Description: "Still-image canvas background; fit controls crop and containment.",
	},
	{
		ID: "background_video", Zone: ZoneBackground, Kind: "video",
		Description: "Video canvas background; fit and looping are configured separately, with blur_cover retained as a legacy fit alias.",
	},
}
