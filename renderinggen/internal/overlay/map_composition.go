package overlay

import "fmt"

type mapCompositionDefinition struct {
	ID           string
	Zone         string
	Description  string
	MotionTarget string
	MapCount     int
}

var mapCompositionCatalog = []mapCompositionDefinition{
	{
		ID: "one_map", Zone: ZoneMaps, MotionTarget: "map_view",
		Description: "Static maps use the centered map motion family; camera fly-to maps use camera_move.",
		MapCount:    1,
	},
	{
		ID: "two_maps", Zone: ZoneMaps, MotionTarget: "map_view",
		Description: "Two georeferenced maps; each plate keeps its own viewport and selects its own map motion.",
		MapCount:    2,
	},
}

func mapCompositionFor(id string) *mapCompositionDefinition {
	for index := range mapCompositionCatalog {
		if mapCompositionCatalog[index].ID == id {
			return &mapCompositionCatalog[index]
		}
	}
	return nil
}

func validateMapComposition(id string, items []resolvedItem) error {
	if id == "" {
		return nil
	}
	definition := mapCompositionFor(id)
	if definition == nil {
		return fmt.Errorf("overlay: unsupported map_composition_id %q", id)
	}
	mapCount := 0
	for _, item := range items {
		if item.Kind == KindMap {
			mapCount++
		}
	}
	if mapCount != definition.MapCount {
		return fmt.Errorf("overlay: map_composition_id %q requires %d map items, got %d", id, definition.MapCount, mapCount)
	}
	return nil
}
