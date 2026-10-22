package overlay

import (
	"fmt"
	"strings"
)

// cameraControllerSet collects the camera authorities declared by a plan so
// conflicts are decided once, after every item has been read. Collecting by
// controller kind instead of checking during the item loop makes acceptance
// independent of plan item order and keeps the conflict error deterministic.
//
// The precedence between controller kinds is unchanged:
//
//	scene_camera > map camera > entity-style camera > sequence camera
//
// The first three are mutually exclusive; the sequence camera is a fallback
// that only applies when no other controller claimed the plan.
type cameraControllerSet struct {
	scene *SemanticSceneCamera

	mapMove   *SemanticMapCameraMove
	mapItemID string
	mapStart  int64
	mapEnd    int64

	entityMotionID string
	entityItemID   string

	sequenceMotionID string
	sequenceItem     semanticItem
	sequenceStart    int64
	sequenceEnd      int64
}

// observe records the controllers owned by one resolved item. Duplicate
// controllers of the same kind are rejected immediately (the message names
// both items, so it is stable regardless of which item came first); conflicts
// between kinds are left to validate.
func (c *cameraControllerSet) observe(ri resolvedItem, src *semanticPlan) error {
	if c.sequenceMotionID == "" {
		if ri.Item.CameraMotionID != "" {
			c.sequenceMotionID = ri.Item.CameraMotionID
			c.sequenceItem = ri.Item
			c.sequenceStart, c.sequenceEnd = ri.Start, ri.End
		} else if isChainKind(ri.Kind) {
			if motionID, item, start, end, ok := chainSequenceCamera(ri, src.FPSNum, src.FPSDen); ok {
				c.sequenceMotionID, c.sequenceItem = motionID, item
				c.sequenceStart, c.sequenceEnd = start, end
			}
		}
	}

	if (isEntityKind(ri.Kind) || isImageKind(ri.Kind)) && ri.Item.EntityStyleID != "" {
		if style, ok := ResolveEntityStyle(ri.Item.EntityStyleID, src.entityStyleSampleKey(), src.VideoID, ri.Item.ID); ok && style.CameraMotionID != "" {
			if c.entityMotionID != "" {
				return fmt.Errorf("overlay: camera motion on entity item %q conflicts with camera motion on entity item %q; only one scene camera controller is allowed per plan", ri.Item.ID, c.entityItemID)
			}
			c.entityMotionID = style.CameraMotionID
			c.entityItemID = ri.Item.ID
		}
	}

	if ri.Map != nil && ri.Map.Camera != nil {
		if c.mapMove != nil {
			return fmt.Errorf("overlay: map item %q camera move conflicts with map item %q; only one scene camera controller is allowed per plan", ri.Item.ID, c.mapItemID)
		}
		move := *ri.Map.Camera
		c.mapMove = &move
		c.mapItemID = ri.Item.ID
		c.mapStart, c.mapEnd = ri.Start, ri.End
	}
	return nil
}

// validate rejects a plan that claims more than one mutually exclusive camera
// controller. It runs after the item loop so the same plan is accepted or
// rejected regardless of item order.
func (c *cameraControllerSet) validate() error {
	if c.scene != nil && c.entityMotionID != "" {
		return fmt.Errorf("overlay: scene_camera conflicts with camera motion on entity item %q; only one scene camera controller is allowed per plan", c.entityItemID)
	}
	if c.scene != nil && c.mapMove != nil {
		return fmt.Errorf("overlay: scene_camera conflicts with scene camera move on map item %q; only one scene camera controller is allowed per plan", c.mapItemID)
	}
	if c.entityMotionID != "" && c.mapMove != nil {
		return fmt.Errorf("overlay: entity item %q camera motion conflicts with map item %q scene camera move; only one scene camera controller is allowed per plan", c.entityItemID, c.mapItemID)
	}
	return nil
}

// apply compiles the winning controller into the plan. Call validate first
// (apply does it) so no partially built camera survives a conflict.
func (c *cameraControllerSet) apply(plan *Plan, src *semanticPlan) error {
	if err := c.validate(); err != nil {
		return err
	}

	if c.scene != nil && plan.Camera == nil {
		cam, anim, err := compileSceneCamera(c.scene, src.Width, src.Height, int64(src.FPSNum), int64(src.FPSDen), CameraDefaultDistanceZ)
		if err != nil {
			return err
		}
		plan.Camera = cam
		plan.CameraAnimation = anim
		if len(c.scene.DepthGroups) > 0 {
			if err := applyDepthGroups(plan.Layers, c.scene.DepthGroups, CameraDefaultDistanceZ); err != nil {
				return err
			}
		}
	}

	if c.mapMove != nil {
		if err := compileMapCamera(plan, *c.mapMove, c.mapStart, c.mapEnd, src.FPSNum, src.FPSDen); err != nil {
			return err
		}
		for i := range plan.Layers {
			if strings.HasPrefix(plan.Layers[i].ID, c.mapItemID+":map_") {
				plan.Layers[i].StartFrame = c.mapStart
				plan.Layers[i].DurationFrames = c.mapEnd - c.mapStart
			}
		}
	} else if c.entityMotionID != "" && plan.Camera == nil {
		cx := float64(src.Width) / 2
		cy := float64(src.Height) / 2
		plan.Camera = defaultPerspectiveCamera(cx, cy)
		tracks, err := entityCameraTracks(c.entityMotionID, cx, cy)
		if err != nil {
			return err
		}
		if len(tracks) > 0 {
			plan.CameraAnimation = &CameraAnimation{Tracks: tracks}
		}
	}

	if c.sequenceMotionID != "" && c.scene == nil && c.mapMove == nil && c.entityMotionID == "" && plan.CameraAnimation == nil {
		plan.Camera = defaultPerspectiveCamera(0, 0)
		plan.CameraAnimation = &CameraAnimation{Tracks: entitySequenceCameraTracks(c.sequenceItem, c.sequenceStart, c.sequenceEnd, src.Width, src.Height, src.FPSNum, src.FPSDen)}
	}
	return nil
}
