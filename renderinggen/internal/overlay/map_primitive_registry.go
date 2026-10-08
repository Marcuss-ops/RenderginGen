package overlay

// mapLayoutCatalog is the canonical support registry for map primitives.
// Selection-model code only publishes this metadata; it does not own the facts.
var mapLayoutCatalog = []RuntimeMapLayout{
	{ID: "basemap", CompositionID: "one_map", Supported: true, Description: "Full-canvas georeferenced raster plate.", ProjectedMotionTargets: []string{"map_view"}},
	{ID: "pins", CompositionID: "one_map", Supported: true, Description: "One grounded shape layer per declared place.", ProjectedMotionTargets: []string{"map_view"}},
	{ID: "pin_labels", CompositionID: "one_map", Supported: true, Description: "One text layer per pin, placed clear of the pin and its neighbours.", ProjectedMotionTargets: []string{"map_view"}},
	{ID: "attribution", CompositionID: "one_map", Supported: true, Description: "Provider-required credit carried by its own text layer.", ProjectedMotionTargets: []string{}},
	{ID: "camera_fly_to", CompositionID: "one_map", Supported: true, Description: "Continues the map plate through camera_move or Natural Earth fly_to_feature resolution instead of a static motion.", ProjectedMotionTargets: []string{"map_view"}},
	{ID: "route", CompositionID: "one_map", Supported: true, Description: "A great-circle path between grounded WGS84 stops, projected into the certified local raster and revealed with the native trim-path operator.", ProjectedMotionTargets: []string{"map_view"}},
	{ID: "stops", CompositionID: "one_map", Supported: false, Description: "An ordered sequence of itinerary stops.", ProjectedMotionTargets: []string{}, UnavailableReason: "no lowering exists for this map layout yet"},
	{ID: "callout", CompositionID: "one_map", Supported: false, Description: "A connected label box anchored to a place.", ProjectedMotionTargets: []string{}, UnavailableReason: "no lowering exists for this map layout yet"},
}

// runtimeMapLayouts returns an isolated projection, including the mutable target
// slices, so picker callers cannot mutate the registry through a returned value.
func runtimeMapLayouts() []RuntimeMapLayout {
	layouts := append([]RuntimeMapLayout(nil), mapLayoutCatalog...)
	for index := range layouts {
		if layouts[index].ProjectedMotionTargets == nil {
			layouts[index].ProjectedMotionTargets = []string{}
		} else {
			layouts[index].ProjectedMotionTargets = append([]string{}, layouts[index].ProjectedMotionTargets...)
		}
	}
	return layouts
}
