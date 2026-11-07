package overlay

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

type cameraPanCase struct {
	id   string
	axis string
	sign float64
}

// cameraPanCases derives its ids and layout signs from the runtime table, so
// adding or moving a direction does not require editing a second list.
var cameraPanCases = func() []cameraPanCase {
	cases := make([]cameraPanCase, 0, len(cameraPanDirections))
	for _, direction := range cameraPanDirections {
		cases = append(cases, cameraPanCase{direction.id, direction.spec.axis, direction.spec.layoutSign})
	}
	return cases
}()

func TestCameraPanPreviewProjectionMatchesRuntimeTable(t *testing.T) {
	preview := CameraPanDirections()
	if len(preview) != len(cameraPanDirections) {
		t.Fatalf("preview projection has %d directions, runtime table has %d", len(preview), len(cameraPanDirections))
	}
	for index, direction := range cameraPanDirections {
		if preview[index].ID != direction.id || preview[index].Axis != direction.spec.axis {
			t.Errorf("preview direction %d = %+v, want id=%q axis=%q", index, preview[index], direction.id, direction.spec.axis)
		}
	}
}

func TestCameraPanTableIsTheSingleDirectionSource(t *testing.T) {
	if len(cameraPanDirections) != 4 || len(cameraPanMotionIDs) != 4 {
		t.Fatalf("camera-pan table has %d directions and %d ids, want four of each", len(cameraPanDirections), len(cameraPanMotionIDs))
	}
	seen := make(map[string]bool, len(cameraPanDirections))
	for index, direction := range cameraPanDirections {
		if seen[direction.id] {
			t.Errorf("camera-pan table repeats %q", direction.id)
		}
		seen[direction.id] = true
		if got := cameraPanMotionIDs[index]; got != direction.id {
			t.Errorf("camera-pan catalog id %d = %q, want table id %q", index, got, direction.id)
		}
		if got, ok := cameraPanSpecs[direction.id]; !ok || got != direction.spec {
			t.Errorf("camera-pan lookup for %q = %+v/%v, want %+v/true", direction.id, got, ok, direction.spec)
		}
		if direction.spec.axis != "x" && direction.spec.axis != "y" {
			t.Errorf("camera-pan %q uses unsupported axis %q", direction.id, direction.spec.axis)
		}
		if direction.spec.travelSign != -direction.spec.layoutSign {
			t.Errorf("camera-pan %q travel sign %v must oppose layout sign %v", direction.id, direction.spec.travelSign, direction.spec.layoutSign)
		}
	}
}

func TestCameraPanFamilyMatchesTheCatalogReusableFamily(t *testing.T) {
	for _, id := range cameraPanMotionIDs {
		definition, err := resolveMotionDefinition(id)
		if err != nil {
			t.Fatalf("resolve camera-pan %q: %v", id, err)
		}
		if !isCameraPanGroup(definition) {
			t.Errorf("camera-pan table id %q is not a reusable camera-pan group", id)
		}
	}
	var catalogPans []string
	for _, id := range motion.ReusableImageGroupMotionIDs() {
		if strings.HasPrefix(id, "image_group_camera_pan_") {
			catalogPans = append(catalogPans, id)
		}
	}
	tablePans := append([]string(nil), cameraPanMotionIDs...)
	sort.Strings(tablePans)
	if fmt.Sprint(catalogPans) != fmt.Sprint(tablePans) {
		t.Fatalf("catalog camera-pan family = %v, runtime table = %v", catalogPans, tablePans)
	}
}

// cameraPanPlan builds a strip plan: one image for the single-image use case,
// N image layers for the group use case. The position override pins the base
// at the canvas centre; the recipe owns the pan offsets on top of it.
func cameraPanPlan(count int, motionID string) []byte {
	var assets, layers []string
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("image_%d", index)
		assets = append(assets, fmt.Sprintf(`{"asset_id":%q,"sha256":%q,"url":%q,"media_type":"image/png"}`,
			id, strings.Repeat("a", 64), "assets/semantic/"+id+".png"))
		layers = append(layers, fmt.Sprintf(`{"id":%q,"asset_id":%q,"start_ms":0,"end_ms":4000,"preset_id":"image_focus_in","motion_id":"","params":{"position_x":0,"position_y":0}}`, id, id))
	}
	if count == 1 {
		return []byte(fmt.Sprintf(`{
			"schema_version":"renderinggen.overlay-plan.v1","plan_id":"camera-pan-single","video_id":"camera-pan-single",
			"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":4000,
			"items":[{"id":"image","kind":"image","template_id":"image_popup","preset_id":"image_focus_in",
			"composition_id":"single_image","motion_id":%q,"start_ms":0,"end_ms":4000,
			"params":{"position_x":0,"position_y":0},"asset_refs":[%s]}]}`, motionID, assets[0]))
	}
	return []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"camera-pan-strip","video_id":"camera-pan-strip",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":4000,
		"items":[{"id":"strip","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
		"composition_id":"multiple_images","group_motion_id":%q,"start_ms":0,"end_ms":4000,
		"asset_refs":[%s],"image_layers":[%s]}]}`,
		motionID, strings.Join(assets, ","), strings.Join(layers, ",")))
}

func cameraPanImages(t *testing.T, count int, motionID string) []Layer {
	t.Helper()
	result, err := CompileSemantic(cameraPanPlan(count, motionID))
	if err != nil {
		t.Fatalf("compile %s with %d images: %v", motionID, count, err)
	}
	var images []Layer
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" {
			images = append(images, layer)
		}
	}
	if len(images) != count {
		t.Fatalf("compiled %d image layers, want %d: %+v", len(images), count, result.Plan.Layers)
	}
	return images
}

func cameraPanLayerTrack(t *testing.T, layer Layer, property string) AnimationTrack {
	t.Helper()
	if layer.Animation == nil || len(layer.Animation.Tracks) != 4 {
		t.Fatalf("layer %q has no four-track strip animation: %+v", layer.ID, layer.Animation)
	}
	for _, track := range layer.Animation.Tracks {
		if track.Property == property {
			return track
		}
	}
	t.Fatalf("layer %q has no %s track: %+v", layer.ID, property, layer.Animation.Tracks)
	return AnimationTrack{}
}

func cameraPanAxis(layer Layer, axis string) float64 {
	if axis == "y" {
		return layer.Position[1]
	}
	return layer.Position[0]
}

// TestCameraPanDirectionsHoldThenTravelToTheNextImage pins the classic move in
// both axes: the strip is one canvas step per member, image zero starts
// centred, and the shared pan track holds for 40% of every step before the
// slower 60% travel to the next image.
func TestCameraPanDirectionsHoldThenTravelToTheNextImage(t *testing.T) {
	for _, tc := range cameraPanCases {
		t.Run(tc.id, func(t *testing.T) {
			images := cameraPanImages(t, 3, tc.id)
			spacing := 640.0
			if tc.axis == "y" {
				spacing = 360
			}
			for index, layer := range images {
				if got, want := cameraPanAxis(layer, tc.axis), tc.sign*float64(index)*spacing; got != want {
					t.Errorf("image %d strip %s = %v, want %v", index, tc.axis, got, want)
				}
				other := "y"
				if tc.axis == "y" {
					other = "x"
				}
				if got := cameraPanAxis(layer, other); got != 0 {
					t.Errorf("image %d leaked %s offset %v; want the neutral axis", index, other, got)
				}
				if layer.StartFrame != 0 || layer.DurationFrames != 96 {
					t.Errorf("image %d window = [%d,%d), want the full group window [0,96)", index, layer.StartFrame, layer.StartFrame+layer.DurationFrames)
				}
				if layer.Enable3D {
					t.Errorf("image %d strip layer must stay flat", index)
				}
				pan := cameraPanLayerTrack(t, layer, "position_"+tc.axis)
				if pan.Easing != "in_out_sine" {
					t.Errorf("image %d pan easing = %q, want the softer in_out_sine", index, pan.Easing)
				}
				if len(pan.Keyframes) != 6 {
					t.Fatalf("image %d pan keyframes = %+v, want the shared six-key staircase", index, pan.Keyframes)
				}
				// 96 frames / 3 members = 32-frame steps; 60% travel is 19
				// frames, leaving a 13-frame hold before each arrival.
				want := []struct {
					frame int64
					value float64
				}{
					{0, 0}, {13, 0}, {32, -tc.sign * spacing},
					{45, -tc.sign * spacing}, {64, -tc.sign * 2 * spacing}, {95, -tc.sign * 2 * spacing},
				}
				for keyIndex, pair := range want {
					key := pan.Keyframes[keyIndex]
					if key.Frame != pair.frame {
						t.Errorf("image %d pan key %d frame = %d, want %d", index, keyIndex, key.Frame, pair.frame)
					}
					value, _ := numericValue(key.Value)
					if value != pair.value {
						t.Errorf("image %d pan key %d value = %v, want %v", index, keyIndex, value, pair.value)
					}
				}
				scale := cameraPanLayerTrack(t, layer, "scale")
				if first, _ := numericValue(scale.Keyframes[0].Value); first != 1.02 {
					t.Errorf("image %d push-in starts at %v, want 1.02", index, first)
				}
				if last, _ := numericValue(scale.Keyframes[len(scale.Keyframes)-1].Value); last != 1.06 {
					t.Errorf("image %d push-in ends at %v, want the closer 1.06", index, last)
				}
			}
		})
	}
}

// TestCameraPanDirectionsApplyToASingleImage proves every direction is
// selectable when only one image exists: the top-level tracks settle the frame
// from the authored camera offset and add the slow push-in without needing a
// second image.
// TestCameraPanFamilyIsOptedIntoAutomaticImagePool pins the runtime inclusion:
// the four pan directions are automatically selectable, while the other group
// recipes stay explicit-only because they do not support a one-image item.
func TestCameraPanFamilyIsOptedIntoAutomaticImagePool(t *testing.T) {
	if !sort.StringsAreSorted(runtimeDefaultImageMotionIDs) {
		t.Fatal("automatic image pool must remain sorted for stable selection")
	}
	seen := make(map[string]bool, len(runtimeDefaultImageMotionIDs))
	for _, id := range runtimeDefaultImageMotionIDs {
		if seen[id] {
			t.Errorf("automatic image pool repeats motion %q", id)
		}
		seen[id] = true
		if strings.HasPrefix(id, "image_group_") && !containsString(cameraPanMotionIDs, id) {
			t.Errorf("group recipe %q outside the camera-pan family entered the automatic image pool", id)
		}
	}
	for _, id := range cameraPanMotionIDs {
		if !seen[id] {
			t.Errorf("camera-pan direction %q is missing from the automatic image pool", id)
		}
	}
}

func TestCameraPanDirectionsApplyToASingleImage(t *testing.T) {
	for _, tc := range cameraPanCases {
		t.Run(tc.id, func(t *testing.T) {
			image := cameraPanImages(t, 1, tc.id)[0]
			pan := cameraPanLayerTrack(t, image, "position_"+tc.axis)
			first, _ := numericValue(pan.Keyframes[0].Value)
			if first != tc.sign*140 {
				t.Errorf("single-image pan starts at %v, want the 140px %s camera offset", first, tc.axis)
			}
			var settled, scaled bool
			for _, key := range pan.Keyframes {
				if key.Frame != 59 {
					continue
				}
				value, _ := numericValue(key.Value)
				if value != 0 {
					t.Errorf("single-image pan settles at %v, want the centred 0", value)
				}
				settled = true
			}
			if !settled {
				t.Fatalf("single-image pan has no frame-59 settle key: %+v", pan.Keyframes)
			}
			scale := cameraPanLayerTrack(t, image, "scale")
			for _, key := range scale.Keyframes {
				if key.Frame != 0 {
					continue
				}
				if value, _ := numericValue(key.Value); value != 1.06 {
					t.Errorf("single-image push-in starts at %v, want the closer 1.06", value)
				}
			}
			for _, key := range scale.Keyframes {
				if key.Frame == 59 {
					if value, _ := numericValue(key.Value); value != 1.0 {
						t.Errorf("single-image push-in settles at %v, want the resting 1.0", value)
					}
					scaled = true
				}
			}
			if !scaled {
				t.Fatalf("single-image push-in has no frame-59 settle key: %+v", scale.Keyframes)
			}
		})
	}
}
