package overlay

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// The camera-pan family is four reusable image-group choreographies. Members
// sit on one strip spaced by a full canvas width (or height); the strip steps
// against the camera direction instead of each member fading in. That reads
// exactly like a camera that holds on the centred image and then travels to
// reveal the next one, without moving the single scene-camera controller.
const (
	imageGroupCameraPanRightMotionID = "image_group_camera_pan_right"
	imageGroupCameraPanLeftMotionID  = "image_group_camera_pan_left"
	imageGroupCameraPanDownMotionID  = "image_group_camera_pan_down"
	imageGroupCameraPanUpMotionID    = "image_group_camera_pan_up"
)

// cameraPanSpec describes one certified direction. layoutSign places later
// members in the direction the camera will travel; travelSign moves the strip
// the opposite way, so a member arrives at the canvas centre. For a rightward
// camera the next portrait is laid out to the right and the strip travels left.
type cameraPanSpec struct {
	axis       string
	layoutSign float64
	travelSign float64
}

// cameraPanDirections is the single source for the four-direction family.
// The lookup map, the catalog-ordered id list and the tests all derive from
// this table instead of restating the ids or the axis/sign semantics. These
// are group recipes, but each direction also supports a one-image item and
// settles at the neutral pose, so they are the only group recipes explicitly
// admitted to the automatic single-image runtime pool.
var cameraPanDirections = []cameraPanDirection{
	{imageGroupCameraPanDownMotionID, cameraPanSpec{axis: "y", layoutSign: 1, travelSign: -1}},
	{imageGroupCameraPanLeftMotionID, cameraPanSpec{axis: "x", layoutSign: -1, travelSign: 1}},
	{imageGroupCameraPanRightMotionID, cameraPanSpec{axis: "x", layoutSign: 1, travelSign: -1}},
	{imageGroupCameraPanUpMotionID, cameraPanSpec{axis: "y", layoutSign: -1, travelSign: 1}},
}

type cameraPanDirection struct {
	id   string
	spec cameraPanSpec
}

var (
	cameraPanSpecs, cameraPanMotionIDs = func() (map[string]cameraPanSpec, []string) {
		specs := make(map[string]cameraPanSpec, len(cameraPanDirections))
		ids := make([]string, 0, len(cameraPanDirections))
		for _, direction := range cameraPanDirections {
			specs[direction.id] = direction.spec
			ids = append(ids, direction.id)
		}
		return specs, ids
	}()
)

// CameraPanDirection is the read-only preview projection of one runtime
// camera-pan direction. Runtime compilation keeps the signs and recipe private.
type CameraPanDirection struct {
	ID   string
	Axis string
}

// CameraPanDirections returns the four runtime directions in table order.
// Preview tools such as cmd/camera-pan-demo consume this projection instead of
// maintaining their own id/axis list.
func CameraPanDirections() []CameraPanDirection {
	directions := make([]CameraPanDirection, 0, len(cameraPanDirections))
	for _, direction := range cameraPanDirections {
		directions = append(directions, CameraPanDirection{ID: direction.id, Axis: direction.spec.axis})
	}
	return directions
}

func cameraPanSpecFor(definition *motion.MotionDefinition) (cameraPanSpec, bool) {
	if definition == nil || definition.GroupRecipe == nil {
		return cameraPanSpec{}, false
	}
	spec, ok := cameraPanSpecs[definition.ID]
	return spec, ok
}

func isCameraPanGroup(definition *motion.MotionDefinition) bool {
	_, ok := cameraPanSpecFor(definition)
	return ok
}

// compileCameraPanGroup lowers a horizontal or vertical strip. The plan camera
// is not moved: translating the whole strip is the same on-screen result on the
// flat overlay canvas and keeps the choreography independent from the single
// scene-camera controller slot.
func compileCameraPanGroup(ri resolvedItem, src *semanticPlan, registry *assetRegistry, definition *motion.MotionDefinition) ([]Layer, error) {
	spec, ok := cameraPanSpecFor(definition)
	if !ok {
		return nil, fmt.Errorf("overlay: motion %q is not a camera-pan group", definition.ID)
	}
	recipe := definition.GroupRecipe
	count := len(ri.Item.ImageLayers)
	if err := recipe.Validate(); err != nil {
		return nil, fmt.Errorf("overlay: motion %q has invalid group recipe: %w", definition.ID, err)
	}
	if !recipe.SupportsCount(count) {
		return nil, fmt.Errorf("overlay: motion %q does not support %d image layers", definition.ID, count)
	}
	if src.Width <= 0 || src.Height <= 0 {
		return nil, fmt.Errorf("overlay: motion %q requires a positive canvas", definition.ID)
	}
	duration := ri.End - ri.Start
	spacing := float64(src.Width)
	if spec.axis == "y" {
		spacing = float64(src.Height)
	}
	pan := cameraPanTrack(ri, src, count, spacing, duration, spec)
	assets := resolveAssetPaths(registry, ri.Item.Assets)
	output := make([]Layer, 0, count*2)
	for index, child := range ri.Item.ImageLayers {
		childResolved, image, err := compileStaticImageChild(ri, src, assets, child, index)
		if err != nil {
			return nil, err
		}
		if err := validatePremiumImageDuration(*definition, duration); err != nil {
			return nil, err
		}
		image, err = decorateImageLayerWithItemEffects(childResolved, image)
		if err != nil {
			return nil, err
		}
		// Every member is present for the whole group window: the pan reveals
		// it, so no member needs its own independent entrance.
		image.StartFrame = ri.Start
		image.DurationFrames = duration
		image.Position = stripPosition(image.Position, index, spacing, spec)
		image.Enable3D = false
		image.Animation = stripAnimation(pan, duration, spec.axis)
		output = append(output, image)
		if caption := strings.TrimSpace(child.Caption); caption != "" {
			captionLayer, err := compileImageCaptionLayer(ri, src, childResolved.Item, caption, &image, child.ID, textRoleImageCaption)
			if err != nil {
				return nil, err
			}
			// Captions ride with their own image instead of stacking at the
			// canvas centre while the strip travels.
			captionLayer.StartFrame = ri.Start
			captionLayer.DurationFrames = duration
			captionLayer.Position = stripPosition(captionLayer.Position, index, spacing, spec)
			captionLayer.Enable3D = false
			captionLayer.Animation = stripAnimation(pan, duration, spec.axis)
			output = append(output, captionLayer)
		}
	}
	return output, nil
}

// stripPosition offsets one member to its slot. Image and text layer positions
// are both canvas-relative centres, so adding index*spacing puts member zero at
// the centre and every later member one canvas step along the pan axis.
func stripPosition(position []float64, index int, spacing float64, spec cameraPanSpec) []float64 {
	out := append([]float64(nil), position...)
	for len(out) < 2 {
		out = append(out, 0)
	}
	axis := 0
	if spec.axis == "y" {
		axis = 1
	}
	out[axis] += float64(index) * spec.layoutSign * spacing
	return out
}

// stripAnimation keeps the four-track shape every reusable image-group recipe
// publishes. The selected axis carries the pan, the other axis stays neutral,
// and the shared scale track adds a slow push-in so the move reads closer and
// more premium instead of a flat slide.
func stripAnimation(pan AnimationTrack, duration int64, axis string) *LayerAnimation {
	last := lastValidFrame(duration)
	neutral := func(property string, value float64) AnimationTrack {
		return AnimationTrack{Property: property, Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: value},
			{Frame: last, Value: value},
		}}
	}
	positionX := neutral("position_x", 0)
	positionY := neutral("position_y", 0)
	if axis == "y" {
		positionY = pan
	} else {
		positionX = pan
	}
	scale := AnimationTrack{Property: "scale", Easing: "in_out_sine", Keyframes: []AnimationKeyframe{
		{Frame: 0, Value: 1.02},
		{Frame: last, Value: 1.06},
	}}
	return &LayerAnimation{Tracks: []AnimationTrack{
		positionX,
		positionY,
		scale,
		neutral("opacity", 1),
	}}
}

// cameraPanTrack builds the shared staircase. The first image holds at the
// centre, then each later image's arrival defines one travel step followed by a
// hold. Travel takes 60% of every step, leaving a deliberate 40% hold for the
// slower editorial cadence.
func cameraPanTrack(ri resolvedItem, src *semanticPlan, count int, spacing float64, duration int64, spec cameraPanSpec) AnimationTrack {
	property := "position_x"
	if spec.axis == "y" {
		property = "position_y"
	}
	track := AnimationTrack{Property: property, Easing: "in_out_sine"}
	if duration <= 0 {
		track.Keyframes = []AnimationKeyframe{{Frame: 0, Value: 0.0}}
		return track
	}
	keys := []AnimationKeyframe{{Frame: 0, Value: 0.0}}
	lastFrame := int64(0)
	step := duration / int64(count)
	if step < 1 {
		step = 1
	}
	travel := step * 3 / 5
	if travel < 1 {
		travel = 1
	}
	arrivals := cameraPanArrivals(ri, src, count, duration)
	for index := 1; index < count; index++ {
		arrival := arrivals[index]
		gap := arrival - lastFrame
		if gap < 2 {
			gap = 2
		}
		stepTravel := travel
		if stepTravel > gap*3/5 {
			stepTravel = gap * 3 / 5
		}
		if stepTravel < 1 {
			stepTravel = 1
		}
		moveStart := arrival - stepTravel
		if moveStart <= lastFrame {
			moveStart = lastFrame + 1
		}
		if moveStart >= arrival {
			arrival = moveStart + 1
		}
		if arrival >= duration {
			arrival = duration - 1
		}
		if arrival <= moveStart {
			break
		}
		keys = append(keys,
			AnimationKeyframe{Frame: moveStart, Value: spec.travelSign * float64(index-1) * spacing},
			AnimationKeyframe{Frame: arrival, Value: spec.travelSign * float64(index) * spacing},
		)
		lastFrame = arrival
	}
	end := lastValidFrame(duration)
	if end > lastFrame {
		keys = append(keys, AnimationKeyframe{Frame: end, Value: spec.travelSign * float64(count-1) * spacing})
	}
	track.Keyframes = keys
	return track
}

// cameraPanArrivals places each member's arrival. Staggered producer start_ms
// values are honoured; a group whose members all start together spreads the
// arrivals evenly across the item window so the classic one-by-one pan still
// reads.
func cameraPanArrivals(ri resolvedItem, src *semanticPlan, count int, duration int64) []int64 {
	arrivals := make([]int64, count)
	staggered := false
	for index, child := range ri.Item.ImageLayers {
		if index == 0 {
			continue
		}
		start, _ := imageLayerFrames(child, int64(src.FPSNum), int64(src.FPSDen))
		if start > 0 {
			staggered = true
		}
		arrivals[index] = start
	}
	step := duration / int64(count)
	if step < 1 {
		step = 1
	}
	for index := 1; index < count; index++ {
		if !staggered {
			arrivals[index] = int64(index) * step
		}
		if arrivals[index] <= arrivals[index-1] {
			arrivals[index] = arrivals[index-1] + step
		}
		if arrivals[index] >= duration {
			arrivals[index] = duration - 1
		}
		if arrivals[index] <= arrivals[index-1] {
			arrivals[index] = arrivals[index-1] + 1
		}
	}
	return arrivals
}
