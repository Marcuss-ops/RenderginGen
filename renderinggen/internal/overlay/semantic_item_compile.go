package overlay

// semantic_item_compile.go lowers the resolved items into layers. It owns the
// per-item contract (compile, optional media stroke, ledger counter, P4 window,
// canvas extension) and the camera observation that must happen for every item
// before the single camera authority is applied. Extracted from
// compileSemanticDecoded so that function stays a list of plan stages.
import "fmt"

func appendResolvedItemLayers(plan *Plan, resolved []resolvedItem, src *semanticPlan, registry *assetRegistry, cameras *cameraControllerSet, stats *Stats) error {
	for _, ri := range resolved {
		if err := cameras.observe(ri, src); err != nil {
			return err
		}
		layers, err := compileItem(ri, src, registry)
		if err != nil {
			return err
		}

		for _, layer := range layers {
			// A premium image motion can expand one item into several linked
			// layers. Attach a clip stroke to the actual image layer only.
			plan.Layers = append(plan.Layers, layer)
			if layer.FrameStroke != nil && layer.Type != "shape" {
				strokeLayer, err := compileMediaStrokeLayer(layer, src.Width, src.Height)
				if err != nil {
					return fmt.Errorf("overlay: item %q: %w", ri.Item.ID, err)
				}
				plan.Layers = append(plan.Layers, strokeLayer)
			}
		}
		// Stroke shapes require render-plan.v3 even though the semantic item
		// itself is a media layer.
		stats.addResolved(ri)
		// Transient per-item window for the P4 segmented-output planner,
		// booked from the SAME resolvedItem the layers were lowered from.
		plan.ItemWindows = append(plan.ItemWindows, ItemWindow{
			ItemID: ri.Item.ID, StartFrame: ri.Start, EndFrame: ri.End})
		if ri.End > plan.Canvas.DurationFrames {
			plan.Canvas.DurationFrames = ri.End
		}
	}
	return nil
}
