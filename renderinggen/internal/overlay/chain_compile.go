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
// item would use.
//
// A chain is one of exactly two choreographies:
//
//   - handoff: independent segment entrances handed off with the chain_camera
//     fly-under dip + fade (compileHandoffChain);
//   - camera pan: the segments become one strip and the strip travels so each
//     segment lands at the centre in turn (compilePanChain).
//
// Both paths share resolveChainSegment, so a segment is decoded, validated and
// turned into the standalone item it was fused from exactly once. The pan
// arithmetic lives in pan_strip.go, the segment wire format in
// chain_segments.go and the chain timeline helpers in chain_pan.go.

import (
	"fmt"
	"strings"
)

// compileChainItem validates the chain container (wire format, map contract,
// camera-pan shape) and dispatches it to its one choreography.
func compileChainItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	segments, err := chainSegmentsFor(ri.Item, ri.Params)
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
	if panMode {
		return compilePanChain(ri, src, registry, segments, panSpec)
	}
	return compileHandoffChain(ri, src, registry, segments)
}

// resolveChainSegment rebuilds one segment as the standalone semantic item it
// was fused from and validates it exactly like a standalone item. This is the
// single per-segment lowering shared by the handoff and pan choreographies;
// only the strip-specific presentation (transition stamp, pan offset) differs
// afterwards.
//
// stripPan clears the segment's own scene camera: the strip owns the camera,
// and a second controller would fight the group track.
func resolveChainSegment(ri resolvedItem, src *semanticPlan, registry *assetRegistry, seg chainSegment, n int, stripPan bool) (resolvedItem, ItemKind, error) {
	if seg.DurationUS <= 0 || seg.StartUS < 0 || seg.StartUS > int64(^uint64(0)>>1)-seg.DurationUS {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d has invalid timing", ri.Item.ID, n)
	}
	segEndUS := seg.StartUS + seg.DurationUS
	chainStartUS := ri.Item.StartMS * 1000
	chainEndUS := ri.Item.EndMS * 1000
	if ri.Item.StartUS != nil && ri.Item.DurationUS != nil {
		chainStartUS = *ri.Item.StartUS
		chainEndUS = *ri.Item.StartUS + *ri.Item.DurationUS
	}
	if seg.StartUS < chainStartUS || segEndUS > chainEndUS {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d [%d,%d) us escapes the chain window [%d,%d) us", ri.Item.ID, n, seg.StartUS, segEndUS, chainStartUS, chainEndUS)
	}
	start, end := usFrames(seg.StartUS, segEndUS, int64(src.FPSNum), int64(src.FPSDen))
	if end <= start {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d collapses to no frames", ri.Item.ID, n)
	}
	segID := fmt.Sprintf("%s:seg%d", ri.Item.ID, n)
	segItem, err := segRIItem(seg, ri.Item, ri.Item.ID, n)
	if err != nil {
		return resolvedItem{}, "", err
	}
	segItem.ID = segID
	segSpec := templateSpecFor(segItem.Template)
	segKind, err := segSpec.resolveKind(segItem.Kind, segID)
	if err != nil {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
	}
	if strings.TrimSpace(segItem.Template) != "" && !segSpec.Registered {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d names unknown template %q", ri.Item.ID, n, segItem.Template)
	}
	if err := validateTextElementContract(segItem); err != nil {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
	}
	if err := validateSemanticImageLayers(segItem, segKind); err != nil {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
	}
	if err := validateMapContract(segItem, segKind, src.Width, src.Height); err != nil {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
	}
	if isImageKind(segKind) {
		segSpec.Family = PresetImage
	} else if segSpec.Kind == KindPrimitive || segKind == KindNumber || segKind == KindMetricStat || segKind == KindTimelineDate {
		segSpec.Family = PresetText
	}
	segParams := mergeSemanticParams(segItem.Params, segItem.Style)
	segRI := resolvedItem{Item: segItem, Spec: segSpec, Kind: segKind, Params: segParams, Start: start, End: end}
	if preset, err := presetFor(segItem, segSpec); err != nil {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
	} else if preset != "" {
		segRI.PresetID = preset
		def, err := resolveOfficialPreset(preset, string(segSpec.Family))
		if err != nil {
			return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		segRI.Preset = def
	}
	if err := validateChildCaptionStyles(segItem); err != nil {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
	}
	segHasImage := len(segItem.Assets) > 0
	segHasCaption := itemHasCaption(segItem)
	segParams = segParamsForKind(segParams, segKind, segHasImage, segHasCaption)
	if err := validateTextRuntimeOverrides(segParams, segID, segKind, segRI.PresetID, segHasImage, segHasCaption); err != nil {
		return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
	}
	segRI.Params = segParams
	// A chain owns N independent photos, so a chain-level image_backdrop is
	// projected onto every image segment and blurred behind its own card.
	// A segment that declares its own block keeps it. A camera-pan strip is the
	// set, so no blurred backdrop may ride on any of its members.
	if stripPan && isImageKind(segKind) {
		delete(segParams, "image_backdrop")
		segRI.Params = segParams
	}
	if isMapKind(segKind) {
		resolved, err := resolveMap(segItem.Map, segItem.Assets, src.Width, src.Height, end-start)
		if err != nil {
			return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d resolve: %w", ri.Item.ID, n, err)
		}
		segRI.Map = resolved
	}
	if (isEntityKind(segKind) || (isImageKind(segKind) && segItem.EntityStyleID != "")) && segItem.ImagePresetID != "" && len(segItem.Assets) > 0 {
		def, err := resolveOfficialPreset(segItem.ImagePresetID, string(PresetImage))
		if err != nil {
			return resolvedItem{}, "", fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		segRI.ImagePreset = def
	}
	return segRI, segKind, nil
}

// compileHandoffChain lowers N independent segment entrances into the same
// pass. Every segment after the first receives the fly-under dip + fade, which
// the daemon executes natively (NativeBlend) or on the CPU fallback.
func compileHandoffChain(ri resolvedItem, src *semanticPlan, registry *assetRegistry, segments []chainSegment) ([]Layer, error) {
	out := make([]Layer, 0, len(segments))
	for n, seg := range segments {
		segRI, segKind, err := resolveChainSegment(ri, src, registry, seg, n, false)
		if err != nil {
			return nil, err
		}
		// Lower through the same dispatcher as standalone items (map and
		// entity branches included); segment kinds are never chains, so
		// this recurses at most one level.
		layers, err := compileItem(segRI, src, registry)
		if err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		// The camera passes under between segments: stamp the fly-under
		// dip + fade on every non-first segment's layers so the handoff
		// reads as one continuous shot. The fade is clamped to the
		// segment length so short segments still decode (the daemon
		// clamps degenerate windows to a single-frame cut itself).
		// A map's own camera_move is its entrance animation. Applying the
		// generic fly-under transition to its georeferenced layers compounds
		// a second camera movement and makes the map smear/double during entry.
		if n > 0 && !isMapKind(segKind) {
			fadeFrames := chainHandoffFadeFrames(int64(src.FPSNum), int64(src.FPSDen), segRI.End-segRI.Start)
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

// chainHandoffFadeFrames is the 12-frame-at-24fps fly-under duration clamped to
// the segment so a short segment still decodes.
func chainHandoffFadeFrames(fpsNum, fpsDen, segFrames int64) int64 {
	fadeFrames := int64(12)
	if fpsNum > 0 && fpsDen > 0 {
		fadeFrames = fpsNum * 12 / 24 / fpsDen
		if fadeFrames < 6 {
			fadeFrames = 6
		}
		if fadeFrames > 24 {
			fadeFrames = 24
		}
	}
	if fadeFrames > segFrames {
		fadeFrames = segFrames
	}
	return fadeFrames
}

// compilePanChain lowers the chain as ONE camera-pan strip. Segments must be
// single-asset image segments; each member's arrival is its own segment start,
// so the strip reaches the centre exactly when that segment begins.
func compilePanChain(ri resolvedItem, src *semanticPlan, registry *assetRegistry, segments []chainSegment, panSpec cameraPanSpec) ([]Layer, error) {
	// One canvas step per member keeps the strip glued to the same
	// constant-speed move in every direction (panStripArrivals owns the
	// spacing and the single-asset admission of each member).
	panSpacing, panArrivals, err := panStripArrivals(ri, src, segments, panSpec)
	if err != nil {
		return nil, err
	}
	// No blurred backdrop on a camera-pan chain. Chronon's software blur
	// pass leaves the blurred surface bound to the next image node: the
	// incoming card then renders the backdrop's asset instead of its own
	// (verified at frame 50 with a 1920x1080 blurred layer). The strip is
	// the set; an explicit block is stripped before the segment compiles.
	delete(ri.Params, "image_backdrop")
	chainFrames := ri.End - ri.Start
	clampPanArrivals(panArrivals, chainFrames)
	// One strip for every member: the choreography and the residency windows
	// come from the same panStrip, so a member can never be retired before the
	// keyframe that moves it has finished.
	strip := buildPanStrip(panArrivals, len(segments), chainFrames)
	pan := chainPanTrack(strip, panSpacing, panSpec)
	panStarts, panEnds := strip.windows()

	// Pan members are laid out on the strip with their authored box, exactly as
	// the historical compile-then-fit flow did, and the probed aspect is applied
	// after the strip has placed them. That order is not cosmetic: the caption
	// is unlinked screen-space text pinned to the resting slot, so fitting first
	// would move it, while fitting afterwards keeps the shipped window/position.
	// overlay still owns the fit; the processor only hands over the numbers.
	blind := *src
	blind.assetSizes = nil

	out := make([]Layer, 0, len(segments))
	for n, seg := range segments {
		segRI, _, err := resolveChainSegment(ri, src, registry, seg, n, true)
		if err != nil {
			return nil, err
		}
		// Lower through the same dispatcher as standalone items (map and
		// entity branches included); segment kinds are never chains, so
		// this recurses at most one level.
		layers, err := compileItem(segRI, &blind, registry)
		if err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		offset := float64(n) * panSpacing * panSpec.layoutSign
		for i := range layers {
			// Images (and their card decorations) live on the strip for the
			// whole chain; a caption only exists during its own segment, so it
			// appears exactly when its image reaches the centre and cannot sit
			// on a neighbouring standalone overlay for the rest of the run.
			isCaption := layers[i].CaptionForImageID != ""
			// A member exists only while the camera can see it (plus the
			// travel overlap with its neighbour), so Chronon never has to keep
			// three full-size off-canvas surfaces resident for the whole
			// chain. Members overlap for the whole handoff: the outgoing image
			// is still on screen while the incoming one travels in, so the
			// chain never falls back to the bare background.
			start, end := panMemberWindow(ri.Start, ri.End, segRI.Start, segRI.End, ri.Start+panStarts[n], ri.Start+panEnds[n], isCaption)
			layers[i].StartFrame = start
			layers[i].DurationFrames = end - start
			if isCaption {
				presentPanCaptionLayer(&layers[i])
			} else {
				presentPanImageLayer(&layers[i], panSpec.axis, offset, pan, chainFrames)
			}
			layers[i].TextAnimators = nil
			// A projected image gets a tight producer surface; a flat screen-
			// space animated image is forced to a full-canvas surface by the
			// native video lane, and two of those exhaust the graph slots.
			layers[i].Enable3D = false
			layers[i].TransitionIn = nil
		}
		out = append(out, layers...)
	}
	if src.assetSizes != nil {
		strip := &Plan{Canvas: Canvas{Width: src.Width, Height: src.Height}, Layers: out}
		if _, err := FitImageLayersToSizes(strip, src.assetSizes); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q camera-pan fit: %w", ri.Item.ID, err)
		}
		out = strip.Layers
	}
	// The strip is one overlapping full-canvas choreography: the processor
	// must not route it through the native residency gate. Record the
	// capability explicitly instead of leaving the processor to infer it from
	// transient layer metadata.
	src.features.overlappingFullCanvasStrip = true
	return out, nil
}

// panStripArrivals projects the chain segments onto the strip: one canvas step
// per member so the travel speed is the same in every direction, and each
// member's own start becomes its arrival.
func panStripArrivals(ri resolvedItem, src *semanticPlan, segments []chainSegment, panSpec cameraPanSpec) (float64, []int64, error) {
	panSpacing := float64(src.Width)
	if panSpec.axis == "y" {
		panSpacing = float64(src.Height)
	}
	arrivals := make([]int64, len(segments))
	for n, seg := range segments {
		kind, _, err := segmentIdentity(seg, ri.Item.ID, n)
		if err != nil {
			return 0, nil, err
		}
		if !isImageKind(ItemKind(kind)) || len(seg.Assets) != 1 || len(seg.ImageLayers) != 0 || seg.Map != nil {
			return 0, nil, fmt.Errorf("overlay: chain item %q segment %d cannot join a camera pan: it must be a single-asset image segment", ri.Item.ID, n)
		}
		startFrame, _ := usFrames(seg.StartUS, seg.StartUS+seg.DurationUS, int64(src.FPSNum), int64(src.FPSDen))
		arrivals[n] = startFrame - ri.Start
		if arrivals[n] < 0 {
			arrivals[n] = 0
		}
	}
	return panSpacing, arrivals, nil
}

// clampPanArrivals makes the arrivals monotonic and keeps every one inside the
// chain window, so a hand-authored segment collision can never invert a move or
// park a member past the item end.
func clampPanArrivals(arrivals []int64, chainFrames int64) {
	for n := 1; n < len(arrivals); n++ {
		if arrivals[n] <= arrivals[n-1] {
			arrivals[n] = arrivals[n-1] + 1
		}
		if chainFrames > 1 && arrivals[n] >= chainFrames {
			arrivals[n] = chainFrames - 1
		}
	}
}

// panMemberWindow resolves one member's [start,end) residency. Images hold the
// strip window so the handoff never falls back to the bare background; a
// caption exists only for its own segment and must be gone before its image's
// surface is retired, because ending both on the same frame is the native
// residency race this trim exists to avoid.
func panMemberWindow(itemStart, itemEnd, segStart, segEnd, windowStart, windowEnd int64, caption bool) (int64, int64) {
	if windowStart < itemStart {
		windowStart = itemStart
	}
	if windowEnd > itemEnd {
		windowEnd = itemEnd
	}
	if windowEnd <= windowStart {
		windowEnd = windowStart + 1
	}
	if !caption {
		return windowStart, windowEnd
	}
	captionStart, captionEnd := segStart, segEnd
	if captionEnd > itemEnd {
		captionEnd = itemEnd
	}
	if captionEnd > windowEnd {
		captionEnd = windowEnd
	}
	if captionEnd-captionStart > 2 {
		captionEnd -= 2
	}
	if captionEnd <= captionStart {
		captionEnd = captionStart + 1
	}
	return captionStart, captionEnd
}

// presentPanImageLayer places one strip member: the shared travel offset, the
// shared position track rebased onto the member's own start, and the strip
// translation the processor uses to keep a fitted caption aligned.
func presentPanImageLayer(layer *Layer, axis string, offset float64, pan AnimationTrack, chainFrames int64) {
	layer.Position = offsetChainPosition(layer.Position, axis, offset)
	// Chronon evaluates a layer's tracks in layer-local frames; the shared
	// strip track is authored on the chain timeline, so shift it back by this
	// member's start before attaching it.
	layer.Animation = rebaseLayerAnimation(panAnimation(pan, chainFrames, axis, nil), layer.StartFrame)
	if layer.Animation != nil {
		tracks := layer.Animation.Tracks[:0]
		for _, track := range layer.Animation.Tracks {
			if track.Property != "opacity" {
				tracks = append(tracks, track)
			}
		}
		layer.Animation.Tracks = tracks
	}
	// No opacity gate on a moving member: Chronon's software dirty-rect pass
	// can composite only part of the previous frame while the opacity and
	// position tracks are both live, tearing the card across the move. The next
	// card peeking at the canvas edge is the strip, not a bug.
	layer.ChainPanOffset = []float64{0, 0}
	if axis == "y" {
		layer.ChainPanOffset[1] = offset
	} else {
		layer.ChainPanOffset[0] = offset
	}
}

// presentPanCaptionLayer keeps a caption at its segment's resting slot: it is
// already centred when its image arrives and never needs a full-canvas moving
// text surface, which Chronon can release late while the composite still
// samples it. A pre-start layer is sampled at local frame 0, so the one-frame
// opacity ramp is what keeps it invisible before its own segment.
func presentPanCaptionLayer(layer *Layer) {
	layer.Position = append([]float64(nil), layer.Position...)
	captionLast := layer.DurationFrames - 1
	if captionLast < 1 {
		captionLast = 1
	}
	layer.Animation = &LayerAnimation{Tracks: []AnimationTrack{{
		Property: "opacity",
		Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: 0.0},
			{Frame: 1, Value: 1.0},
			{Frame: captionLast, Value: 1.0},
		},
	}}}
	layer.ChainPanOffset = nil
	layer.CaptionForImageID = ""
}
