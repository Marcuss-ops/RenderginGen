package overlay

// chain_compile.go owns the Multi-Animations Runtime lowering: one
// phrase_chain / image_chain / mixed_chain item becomes N timed layers
// inside the SAME Chronon render pass, each with its own motion.
//
// The producer (BrainRust `coalesce_chained_overlays`, formerly PipelineGen
// CoalesceChainedOverlays) spans the chain from first.Start to last.End and
// rides every segment in Params["chain_segments"]
// (absolute plan microseconds + per-segment text/motion/preset/asset). This
// file never re-derives editorial timing: it offsets each segment against the
// chain window and lowers it through the SAME per-kind compiler a standalone
// item would use. Segments keep their own entrance motions and hand off with
// the chain_camera fly-under dip + fade: every non-first segment's layers
// carry TransitionIn fly_under_fade, which the daemon executes natively
// (NativeBlend) or on the CPU fallback.
//
// File layout: this file owns compileChainItem; chain_segments.go decodes and
// validates the segment wire format and chain_pan.go owns the pan/timeline
// arithmetic.

import (
	"fmt"
	"strings"
)

// compileChainItem lowers one chain item to N layers in the same pass. Every
// segment is rebuilt as its own semantic item (kind/template/text/motion/
// assets/layers/map/caption/style) and lowered through the SAME dispatcher a
// standalone item uses, so numbers, cards, composite images and maps keep
// their own compilers. Every segment after the first receives the fly-under
// dip + fade transition.
func compileChainItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	segments, err := decodeChainSegments(ri.Params, ri.Item.ID)
	if err != nil {
		return nil, err
	}
	// A chain camera-pan turns the image run into ONE continuous camera move:
	// the segments sit on a strip one canvas apart and the strip travels so each
	// segment lands at the centre in turn. That is the group choreography, not N
	// independent single-image entrances. It is opt-in through the producer's
	// `chain_camera` value and only valid for single-asset image segments.
	panSpec, panMode := chainPanSpecFor(ri.Params)
	if panMode && ri.Kind != KindImageChain {
		return nil, fmt.Errorf("overlay: chain item %q chain_camera %q requires an image_chain", ri.Item.ID, ri.Params["chain_camera"])
	}
	panSpacing := 0.0
	var panArrivals []int64
	if panMode {
		// One canvas step per member keeps the strip glued to the same
		// constant-speed move in every direction. The parked neighbour still
		// overlaps the canvas edge, so the compiler gates its opacity until
		// the handoff starts instead of pushing it a fraction of a step out
		// (which made the incoming image travel faster than the strip).
		panSpacing = float64(src.Width)
		if panSpec.axis == "y" {
			panSpacing = float64(src.Height)
		}
		panArrivals = make([]int64, len(segments))
		for n, seg := range segments {
			kind, _, err := segmentIdentity(seg, ri.Item.ID, n)
			if err != nil {
				return nil, err
			}
			if !isImageKind(ItemKind(kind)) || len(seg.Assets) != 1 || len(seg.ImageLayers) != 0 || seg.Map != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d cannot join a camera pan: it must be a single-asset image segment", ri.Item.ID, n)
			}
			startFrame, _ := usFrames(seg.StartUS, seg.StartUS+seg.DurationUS, int64(src.FPSNum), int64(src.FPSDen))
			panArrivals[n] = startFrame - ri.Start
			if panArrivals[n] < 0 {
				panArrivals[n] = 0
			}
		}
		// No blurred backdrop on a camera-pan chain. Chronon's software blur
		// pass leaves the blurred surface bound to the next image node: the
		// incoming card then renders the backdrop's asset instead of its own
		// (verified at frame 50 with a 1920x1080 blurred layer). The strip is
		// the set; an explicit block is stripped before the segment compiles.
		delete(ri.Params, "image_backdrop")
		chainFrames := ri.End - ri.Start
		for n := 1; n < len(panArrivals); n++ {
			if panArrivals[n] <= panArrivals[n-1] {
				panArrivals[n] = panArrivals[n-1] + 1
			}
			if chainFrames > 1 && panArrivals[n] >= chainFrames {
				panArrivals[n] = chainFrames - 1
			}
		}
	}
	// Maps are standalone by contract. A hand-built mixed/image/text chain
	// carrying a map would lower a georeferenced full-canvas segment inside a
	// container that cannot validate its LOD window; reject it before any
	// layer is produced. A map_chain, conversely, is only valid when every
	// segment actually carries a map.
	mapChain := ri.Kind == KindMapChain
	for n, seg := range segments {
		if !mapChain && seg.Map != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d carries a map; maps must stay standalone", ri.Item.ID, n)
		}
		if mapChain && seg.Map == nil {
			return nil, fmt.Errorf("overlay: map chain item %q segment %d has no map payload", ri.Item.ID, n)
		}
	}
	out := make([]Layer, 0, len(segments))
	for n, seg := range segments {
		if seg.DurationUS <= 0 || seg.StartUS < 0 || seg.StartUS > int64(^uint64(0)>>1)-seg.DurationUS {
			return nil, fmt.Errorf("overlay: chain item %q segment %d has invalid timing", ri.Item.ID, n)
		}
		segEndUS := seg.StartUS + seg.DurationUS
		chainStartUS := ri.Item.StartMS * 1000
		chainEndUS := ri.Item.EndMS * 1000
		if ri.Item.StartUS != nil && ri.Item.DurationUS != nil {
			chainStartUS = *ri.Item.StartUS
			chainEndUS = *ri.Item.StartUS + *ri.Item.DurationUS
		}
		if seg.StartUS < chainStartUS || segEndUS > chainEndUS {
			return nil, fmt.Errorf("overlay: chain item %q segment %d [%d,%d) us escapes the chain window [%d,%d) us", ri.Item.ID, n, seg.StartUS, segEndUS, chainStartUS, chainEndUS)
		}
		start, end := usFrames(seg.StartUS, segEndUS, int64(src.FPSNum), int64(src.FPSDen))
		if end <= start {
			return nil, fmt.Errorf("overlay: chain item %q segment %d collapses to no frames", ri.Item.ID, n)
		}
		segID := fmt.Sprintf("%s:seg%d", ri.Item.ID, n)
		segItem, err := segRIItem(seg, ri.Item, ri.Item.ID, n)
		if err != nil {
			return nil, err
		}
		segItem.ID = segID
		segSpec := templateSpecFor(segItem.Template)
		segKind, err := segSpec.resolveKind(segItem.Kind, segID)
		if err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if strings.TrimSpace(segItem.Template) != "" && !segSpec.Registered {
			return nil, fmt.Errorf("overlay: chain item %q segment %d names unknown template %q", ri.Item.ID, n, segItem.Template)
		}
		if err := validateTextElementContract(segItem); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if err := validateSemanticImageLayers(segItem, segKind); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if err := validateMapContract(segItem, segKind, src.Width, src.Height); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if isImageKind(segKind) {
			segSpec.Family = PresetImage
		} else if segSpec.Kind == KindPrimitive || segKind == KindNumber || segKind == KindMetricStat || segKind == KindTimelineDate {
			segSpec.Family = PresetText
		}
		segParams := mergeSemanticParams(segItem.Params, segItem.Style)
		segRI := resolvedItem{Item: segItem, Spec: segSpec, Kind: segKind, Params: segParams, Start: start, End: end}
		if preset, err := presetFor(segItem, segSpec); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		} else if preset != "" {
			segRI.PresetID = preset
			def, err := resolveOfficialPreset(preset, string(segSpec.Family))
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
			}
			segRI.Preset = def
		}
		if err := validateChildCaptionStyles(segItem); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		segHasImage := len(segItem.Assets) > 0
		segHasCaption := itemHasCaption(segItem)
		segParams = segParamsForKind(segParams, segKind, segHasImage, segHasCaption)
		if err := validateTextRuntimeOverrides(segParams, segID, segKind, segRI.PresetID, segHasImage, segHasCaption); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		segRI.Params = segParams
		// A chain owns N independent photos, so a chain-level image_backdrop is
		// projected onto every image segment and blurred behind its own card.
		// A segment that declares its own block keeps it.
		if panMode && isImageKind(segKind) {
			delete(segParams, "image_backdrop")
			segRI.Params = segParams
		}
		if isMapKind(segKind) {
			resolved, err := resolveMap(segItem.Map, segItem.Assets, src.Width, src.Height, end-start)
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d resolve: %w", ri.Item.ID, n, err)
			}
			segRI.Map = resolved
		}
		if (isEntityKind(segKind) || (isImageKind(segKind) && segItem.EntityStyleID != "")) && segItem.ImagePresetID != "" && len(segItem.Assets) > 0 {
			def, err := resolveOfficialPreset(segItem.ImagePresetID, string(PresetImage))
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
			}
			segRI.ImagePreset = def
		}
		if panMode {
			// The strip owns the camera; a per-segment scene camera would be a
			// second controller and a per-segment position still would fight the
			// group track.
			segItem.CameraMotionID = ""
			segRI.Item.CameraMotionID = ""
		}
		var layers []Layer
		// Lower through the same dispatcher as standalone items (map and
		// entity branches included); segment kinds are never chains, so
		// this recurses at most one level.
		if layers, err = compileItem(segRI, src, registry); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if panMode {
			chainFrames := ri.End - ri.Start
			pan := chainPanTrack(panArrivals, len(segments), panSpacing, chainFrames, panSpec)
			panStarts, panEnds := chainPanWindows(panArrivals, len(segments), chainFrames)
			offset := float64(n) * panSpacing * panSpec.layoutSign
			segStart, segEnd := usFrames(seg.StartUS, seg.StartUS+seg.DurationUS, int64(src.FPSNum), int64(src.FPSDen))
			if segEnd > ri.End {
				segEnd = ri.End
			}
			for i := range layers {
				// Images (and their card decorations) live on the strip for the
				// whole chain; a caption only exists during its own segment, so it
				// appears exactly when its image reaches the centre and cannot sit
				// on a neighbouring standalone overlay for the rest of the run.
				isCaption := layers[i].CaptionForImageID != ""
				// A member exists only while the camera can see it (plus the
				// travel overlap with its neighbour), so Chronon never has to
				// keep three full-size off-canvas surfaces resident for the
				// whole chain.
				imageStart := ri.Start + panStarts[n]
				imageEnd := ri.Start + panEnds[n]
				// Members overlap for the whole handoff: the outgoing image is
				// still on screen while the incoming one travels in, so the
				// chain never falls back to the bare background. Chain-camera
				// plans run on the software encoder (see the execution policy),
				// where two animated full-canvas surfaces are supported.
				if imageStart < ri.Start {
					imageStart = ri.Start
				}
				if imageEnd > ri.End {
					imageEnd = ri.End
				}
				if imageEnd <= imageStart {
					imageEnd = imageStart + 1
				}
				if !isCaption {
					layers[i].StartFrame = imageStart
					layers[i].DurationFrames = imageEnd - imageStart
				} else {
					captionStart := segStart
					captionEnd := segEnd
					// The caption must be gone before its image's surface is
					// retired; ending both on the same frame is the native
					// residency race this trim exists to avoid.
					if captionEnd > imageEnd {
						captionEnd = imageEnd
					}
					if captionEnd-captionStart > 2 {
						captionEnd -= 2
					}
					if captionEnd <= captionStart {
						captionEnd = captionStart + 1
					}
					layers[i].StartFrame = captionStart
					layers[i].DurationFrames = captionEnd - captionStart
				}
				if !isCaption {
					layers[i].Position = offsetChainPosition(layers[i].Position, panSpec.axis, offset)
					// Chronon evaluates a layer's tracks in layer-local frames; the
					// shared strip track is authored on the chain timeline, so shift
					// it back by this member's start before attaching it.
					layers[i].Animation = rebaseLayerAnimation(
						chainPanAnimation(pan, chainFrames, panSpec.axis),
						layers[i].StartFrame,
					)
					if layers[i].Animation != nil {
						tracks := layers[i].Animation.Tracks[:0]
						for _, track := range layers[i].Animation.Tracks {
							if track.Property != "opacity" {
								tracks = append(tracks, track)
							}
						}
						layers[i].Animation.Tracks = tracks
					}
					// No opacity gate on a moving member: Chronon's software
					// dirty-rect pass can composite only part of the previous
					// frame while the opacity and position tracks are both live,
					// tearing the card across the move. The next card peeking at
					// the canvas edge is the strip, not a bug.
					layers[i].ChainPanOffset = []float64{0, 0}
					if panSpec.axis == "y" {
						layers[i].ChainPanOffset[1] = offset
					} else {
						layers[i].ChainPanOffset[0] = offset
					}
				} else {
					// A moving mtsdf text surface can be released late by Chronon
					// while the composite still samples it. Keep the caption at the
					// segment's resting slot: it is already centred when the image
					// arrives and never needs a full-canvas moving text surface.
					layers[i].Position = append([]float64(nil), layers[i].Position...)
					// Captions share the strip timeline but must stay invisible
					// before their own segment: a pre-start layer is sampled at
					// local frame 0, so gate it with a one-frame opacity ramp.
					captionLast := layers[i].DurationFrames - 1
					if captionLast < 1 {
						captionLast = 1
					}
					layers[i].Animation = &LayerAnimation{Tracks: []AnimationTrack{{
						Property: "opacity",
						Keyframes: []AnimationKeyframe{
							{Frame: 0, Value: 0.0},
							{Frame: 1, Value: 1.0},
							{Frame: captionLast, Value: 1.0},
						},
					}}}
					layers[i].ChainPanOffset = nil
					layers[i].CaptionForImageID = ""
				}
				layers[i].TextAnimators = nil
				// A projected image gets a tight producer surface; a flat screen-
				// space animated image is forced to a full-canvas surface by the
				// native video lane, and two of those exhaust the graph slots.
				layers[i].Enable3D = false
				layers[i].TransitionIn = nil
			}
		}
		// The camera passes under between segments: stamp the fly-under
		// dip + fade on every non-first segment's layers so the handoff
		// reads as one continuous shot. The daemon executes it natively
		// (NativeBlend dip offset + opacity) with a CPU fallback, so the
		// stamp is safe on every execution path. The fade is clamped to
		// the segment length so short segments still decode (the daemon
		// clamps degenerate windows to a single-frame cut itself).
		// A map's own camera_move is its entrance animation. Applying the
		// generic fly-under transition to its georeferenced layers compounds
		// a second camera movement and makes the map smear/double during entry.
		if !panMode && n > 0 && !isMapKind(segKind) {
			fadeFrames := int64(12)
			if fps := int64(src.FPSNum); fps > 0 && int64(src.FPSDen) > 0 {
				fadeFrames = fps * 12 / 24 / int64(src.FPSDen)
				if fadeFrames < 6 {
					fadeFrames = 6
				}
				if fadeFrames > 24 {
					fadeFrames = 24
				}
			}
			if segFrames := end - start; fadeFrames > segFrames {
				fadeFrames = segFrames
			}
			for i := range layers {
				layers[i].TransitionIn = &LayerTransition{
					ID: "fly_under_fade", Kind: "fly_under_fade",
					DurationFrames: fadeFrames, Direction: "up",
					Easing: "in_out_cubic",
					Params: map[string]any{"dip_px": 60.0},
				}
			}
		}
		out = append(out, layers...)
	}
	return out, nil
}
