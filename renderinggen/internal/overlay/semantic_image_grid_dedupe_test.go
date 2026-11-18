package overlay

import (
	"fmt"
	"strings"
	"testing"
)

// gridCompositePlan builds one composite image item whose children may each
// declare their own params.grid, plus an optional item-level grid. It is the
// legacy (`image_layers`) path: the one where a per-child emission would draw
// the same full-canvas grid once per member.
func gridCompositePlan(childGrid string, itemParams string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","legacy_composite_replay":true,"plan_id":"grid-dedupe","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"duo","kind":"image","template_id":"image_popup","start_ms":0,"end_ms":5000,"asset_refs":[%s,%s],"image_layers":[{"id":"left","asset_id":"effect-photo","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"%s},{"id":"right","asset_id":"effect-photo-2","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in"%s}]%s}]}`,
		imageEffectsAssetRef, imageEffectsSecondAssetRef, childGrid, childGrid, itemParams))
}

func countImageGridLayers(result CompileResult) []Layer {
	var grids []Layer
	for _, layer := range result.Plan.Layers {
		if strings.HasSuffix(layer.ID, ":image-grid") {
			grids = append(grids, layer)
		}
	}
	return grids
}

// TestCompositeImageGridEmitsOnce is P5: the grid is a full-canvas
// screen-space layer bound to the item lifetime, so a composite must draw one
// copy. Declaring it on every child used to stack N identical grids (double
// opacity, N wasted layers).
func TestCompositeImageGridEmitsOnce(t *testing.T) {
	childGrid := `,"params":{"grid":{"scope":"foreground","style":"lines","color":"#7FA8C8","spacing_px":96,"line_width_px":1,"opacity":0.12}}`
	result, err := CompileSemantic(gridCompositePlan(childGrid, ""))
	if err != nil {
		t.Fatalf("compile composite with per-child grid: %v", err)
	}
	grids := countImageGridLayers(result)
	if len(grids) != 1 {
		t.Fatalf("composite grid layers = %d (%+v), want exactly one", len(grids), grids)
	}
	if grids[0].ID != "duo:left:image-grid" {
		t.Fatalf("first declared child owns the single grid, got %q", grids[0].ID)
	}
}

// TestCompositeImageGridPrefersItemLevelBlock: the producer writes the grid on
// the item, so an item-level block is the copy that survives when a legacy
// child also declares one.
func TestCompositeImageGridPrefersItemLevelBlock(t *testing.T) {
	childGrid := `,"params":{"grid":{"style":"lines","color":"#FFFFFF","spacing_px":32,"line_width_px":1}}`
	itemParams := `,"params":{"grid":{"scope":"foreground","style":"dots","color":"#7FA8C8","spacing_px":96,"dot_radius_px":2,"opacity":0.2}}`
	result, err := CompileSemantic(gridCompositePlan(childGrid, itemParams))
	if err != nil {
		t.Fatalf("compile composite with item-level grid: %v", err)
	}
	grids := countImageGridLayers(result)
	if len(grids) != 1 {
		t.Fatalf("composite grid layers = %d (%+v), want exactly one", len(grids), grids)
	}
	if grids[0].ID != "duo:image-grid" {
		t.Fatalf("item-level grid must win, got %q", grids[0].ID)
	}
	if grids[0].StartFrame != 0 || grids[0].DurationFrames != 120 {
		t.Fatalf("grid must span the item lifetime, got start=%d duration=%d", grids[0].StartFrame, grids[0].DurationFrames)
	}
}
