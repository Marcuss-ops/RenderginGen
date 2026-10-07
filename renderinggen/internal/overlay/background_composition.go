package overlay

type backgroundCompositionDefinition struct {
	ID          string
	Zone        string
	Kind        string
	Description string
	// Supported is a catalog property, not a projection decision: the picker
	// derives every background source — including the declared-but-unsupported
	// ones — from this table, so no alternative "unsupported list" can drift
	// away from the catalog.
	Supported         bool
	UnavailableReason string
	RequiresAssets    bool
	FitPolicy         string
	Loop              bool
	Priority          int
}

var backgroundCompositionCatalog = []backgroundCompositionDefinition{
	{
		ID: "background_color", Zone: ZoneBackground, Kind: "color",
		Description: "RGBA solid-color canvas background.",
		Supported:   true, Priority: 0,
	},
	{
		ID: "background_image", Zone: ZoneBackground, Kind: "image",
		Description: "Still-image canvas background; fit controls crop and containment.",
		Supported:   true, RequiresAssets: true, FitPolicy: "cover", Priority: 1,
	},
	{
		ID: "background_video", Zone: ZoneBackground, Kind: "video",
		Description: "Video canvas background; fit and looping are configured separately, with blur_cover retained as a legacy fit alias.",
		Supported:   true, RequiresAssets: true, FitPolicy: "cover", Loop: true, Priority: 1,
	},
	// Declared-but-unsupported sources stay in the catalog with an explicit
	// reason, exactly as the selection model published them before: visible,
	// never silently hidden, and added by editing this table.
	{
		ID: "background_gradient", Zone: ZoneBackground, Kind: "gradient",
		Description:       "Interpolated color ramp.",
		Priority:          2,
		UnavailableReason: "the background lowering accepts only color, image and video today",
	},
	{
		ID: "background_pattern", Zone: ZoneBackground, Kind: "pattern",
		Description:    "Tiled texture.",
		RequiresAssets: true, Priority: 2,
		UnavailableReason: "the background lowering accepts only color, image and video today",
	},
	{
		ID: "background_generated", Zone: ZoneBackground, Kind: "generated",
		Description:       "Renderer-synthesised abstract plate.",
		Priority:          2,
		UnavailableReason: "the background lowering accepts only color, image and video today",
	},
}
