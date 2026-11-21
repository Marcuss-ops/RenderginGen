package overlay

import "testing"

// TestPanStripIsTheSingleStaircase pins the shared choreography AND its single
// representation: chain and group both bake the sine per frame and declare
// linear easing, so their tracks are identical for identical arrivals. This is
// the regression guard against the two engines drifting apart again — and the
// shape Chronon's component-position compiler can execute (a non-linear axis
// track must share keyframes with the neutral axis or the plan is rejected).
func TestPanStripIsTheSingleStaircase(t *testing.T) {
	arrivals := []int64{0, 32, 64}
	count := 3
	duration := int64(96)
	spacing := 640.0
	spec := cameraPanSpecs[imageGroupCameraPanRightMotionID]

	strip := buildPanStrip(arrivals, count, duration)
	if len(strip.steps) != count-1 {
		t.Fatalf("strip resolved %d steps, want %d: %+v", len(strip.steps), count-1, strip.steps)
	}

	chain := chainPanTrack(strip, spacing, spec)
	group := buildPanStrip(arrivals, count, duration).track("position_x", spacing, spec.travelSign)
	if chain.Property != group.Property || chain.Property != "position_x" {
		t.Fatalf("pan property chain=%q group=%q, want position_x", chain.Property, group.Property)
	}
	for name, track := range map[string]AnimationTrack{"chain": chain, "group": group} {
		if track.Easing != "linear" {
			t.Fatalf("%s pan easing = %q, want the baked linear curve", name, track.Easing)
		}
	}
	if len(chain.Keyframes) != len(group.Keyframes) {
		t.Fatalf("chain/group key counts = %d/%d, want the same baked staircase", len(chain.Keyframes), len(group.Keyframes))
	}
	for index := range chain.Keyframes {
		chainKey, groupKey := chain.Keyframes[index], group.Keyframes[index]
		if chainKey.Frame != groupKey.Frame {
			t.Fatalf("key %d frame: chain %d, group %d", index, chainKey.Frame, groupKey.Frame)
		}
		chainValue, _ := numericValue(chainKey.Value)
		groupValue, _ := numericValue(groupKey.Value)
		if chainValue != groupValue {
			t.Fatalf("key %d value: chain %v, group %v", index, chainValue, groupValue)
		}
	}
	// The bake must actually cover every travelled frame, not just the six
	// boundaries of the staircase.
	if len(chain.Keyframes) <= 2+2*len(strip.steps) {
		t.Fatalf("pan has %d keys, want more than the %d sparse boundaries", len(chain.Keyframes), 2+2*len(strip.steps))
	}

	// Windows are a projection of the same steps: member i is on screen from
	// its travel start, member i-1 retires when member i arrives, and the last
	// member holds to the window end.
	starts, ends := strip.windows()
	if starts[0] != 0 {
		t.Fatalf("first member window starts at %d, want 0", starts[0])
	}
	for _, step := range strip.steps {
		if starts[step.index] != step.start {
			t.Fatalf("member %d window start %d does not match its travel start %d", step.index, starts[step.index], step.start)
		}
		if ends[step.index-1] != step.end {
			t.Fatalf("member %d retires at %d, want the next arrival %d", step.index-1, ends[step.index-1], step.end)
		}
	}
	if ends[count-1] != duration {
		t.Fatalf("last member holds to %d, want %d", ends[count-1], duration)
	}
}

// TestPanStripRejectsInvertedArrivals proves the shared engine fails safe on a
// hand-authored strip whose segment starts collide: the staircase stops instead
// of emitting a backwards move or a negative-length window.
func TestPanStripRejectsInvertedArrivals(t *testing.T) {
	strip := buildPanStrip([]int64{0, 10, 10, 10}, 4, 96)
	last := int64(0)
	for _, step := range strip.steps {
		if step.end <= step.start {
			t.Fatalf("inverted step %+v", step)
		}
		if step.start < last {
			t.Fatalf("step %+v starts before the previous arrival %d", step, last)
		}
		last = step.end
	}
	starts, ends := strip.windows()
	for index := 1; index < len(starts); index++ {
		if ends[index-1] < starts[index] {
			t.Fatalf("member %d window [%d,%d) opens after the next member starts at %d", index-1, starts[index-1], ends[index-1], starts[index])
		}
	}
}

// TestChainCameraPanPublishesTheSoftwareEncodeFeature proves the compiler
// states the execution requirement explicitly: a pan chain seals
// OverlappingFullCanvasStrip on the plan, and an ordinary chain does not.
func TestChainCameraPanPublishesTheSoftwareEncodeFeature(t *testing.T) {
	build := func(params string) []byte {
		return []byte(`{
			"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-feature","video_id":"v",
			"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":9000,
			"items":[{
				"id":"chain-feature-1","kind":"image_chain","template_id":"IMAGE_CHAIN",
				"start_ms":0,"end_ms":9000,
				"asset_refs":[` + panTestAsset("ada") + `,` + panTestAsset("grace") + `],
				"params":{` + params + `
					"chain_count":2,
					"chain_segments":[
						` + panTestSegment("ada", "Ada", 0) + `,
						` + panTestSegment("grace", "Grace", 3_000_000) + `
					]
				}
			}]
		}`)
	}
	pan, err := CompileSemantic(build(`"chain_camera":"image_group_camera_pan_right",`))
	if err != nil {
		t.Fatalf("compile pan chain: %v", err)
	}
	if !pan.Plan.Features.OverlappingFullCanvasStrip {
		t.Fatalf("pan chain features = %+v, want OverlappingFullCanvasStrip", pan.Plan.Features)
	}
	if !pan.Plan.Features.RequiresSoftwareEncode() {
		t.Fatalf("pan chain must require the software encode lane: %+v", pan.Plan.Features)
	}

	handoff, err := CompileSemantic(build(""))
	if err != nil {
		t.Fatalf("compile handoff chain: %v", err)
	}
	if handoff.Plan.Features.OverlappingFullCanvasStrip {
		t.Fatalf("handoff chain unexpectedly requires the software lane: %+v", handoff.Plan.Features)
	}
}
