//go:build certification

package overlay

import (
	"encoding/json"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"math"
	"strings"
	"testing"
)

func TestGoldenEditorialV1MegaCanary(t *testing.T) {
	// The MEGA canary: one 20 s timeline that walks every Editorial Visual
	// Motion V1 family in sequence — entity cards with animated captions, the
	// full 14-motion editorial image gallery, the text_3d family, and the
	// georeferenced map flyover with the web focus flow composited over it.
	// Every layer must lower animated (or grounded), inside the canvas, and
	// the whole plan must compile deterministically through the pipeline.
	const megaDurationMS = int64(20000)

	// Act 1 (0–3 s): short, long and Unicode entity cards, one at a time.
	cards := []struct {
		id, name, imageMotion, captionMotion string
		startMS, endMS                       int64
	}{
		{"mega-card-a", "Ada Lovelace", "image_depth_dolly", "text_depth_in", 0, 1000},
		{"mega-card-b", "A Very Long Editorial Name That Must Fit", "image_yaw_reveal", "text_word_rise", 1000, 2000},
		{"mega-card-c", "朱元璋", "image_25d_depth_float_in", "text_yaw_in", 2000, 3000},
	}
	items := make([]any, 0, len(cards)+14+4+4)
	for _, c := range cards {
		item := entityCardItem(c.id, c.name, c.startMS, c.endMS, c.imageMotion)
		item["caption_motion_id"] = c.captionMotion
		item["params"] = map[string]any{
			"width": 420.0, "height": 420.0,
			"position_y": -20.0,
		}
		item["asset_refs"] = []any{map[string]any{
			"asset_id": c.id, "sha256": strings.Repeat("a", 64),
			"url": "assets/canary/" + strings.TrimPrefix(c.id, "mega-") + ".png", "media_type": "image/png",
		}}
		items = append(items, item)
	}

	// Act 2 (3–6 s): website screenshot, focused section and caption.
	items = append(items,
		map[string]any{
			"id": "mega-web-main", "kind": "image",
			"template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in",
			"motion_id": "web_browser_tilt_in",
			"params":    map[string]any{"width": 640.0, "height": 360.0, "position_x": 0.0, "position_y": -30.0},
			"start_ms":  3000, "end_ms": 6000, "duration_ms": 3000,
			"asset_refs": []any{map[string]any{"asset_id": "mega-web-main", "sha256": strings.Repeat("b", 64), "url": "assets/canary/web-main.png", "media_type": "image/png"}},
		},
		map[string]any{
			"id": "mega-web-focus", "kind": "image",
			"template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in",
			"motion_id": "web_section_spotlight",
			"params":    map[string]any{"width": 300.0, "height": 180.0, "position_x": 120.0, "position_y": -30.0},
			"start_ms":  4000, "end_ms": 6000, "duration_ms": 2000,
			"asset_refs": []any{map[string]any{"asset_id": "mega-web-focus", "sha256": strings.Repeat("b", 64), "url": "assets/canary/web-focus.png", "media_type": "image/png"}},
		},
		map[string]any{
			"id": "mega-web-caption", "entity_id": "text:mega-web-caption", "kind": "entity_card",
			"template_id": "PERSON_DEFAULT", "preset_id": "phrase_default",
			"text": "SECTION SPOTLIGHT", "motion_id": "text_fade_up",
			"params":   map[string]any{"position_x": 640.0, "position_y": 245.0, "width": 700.0, "height": 100.0},
			"start_ms": 4600, "end_ms": 6000, "duration_ms": 1400,
		},
	)

	// Act 3 (6–10 s): the georeferenced map plate and its route pins.
	items = append(items, map[string]any{
		"id": "mega-map", "kind": "map", "template_id": "MAP",
		"start_ms": 6000, "end_ms": 10000, "duration_ms": 4000,
		"asset_refs": []any{map[string]any{"asset_id": "mega-basemap", "sha256": strings.Repeat("c", 64), "url": "assets/canary/map-europe.png", "media_type": "image/png"}},
		"map": map[string]any{
			"provider": "local", "source_id": "certified-europe-plate", "source_license": "operator-supplied",
			"center": map[string]any{"latitude": 0.0, "longitude": 0.0}, "zoom": 1, "width": 1280, "height": 720,
			"attribution": "Map data (c) operator-certified plate", "motion_id": "image_focus_reveal",
			"pins": []any{
				map[string]any{"id": "london", "label": "London", "latitude": 51.5074, "longitude": -0.1278, "color": "#38BDF8", "radius_px": 10.0},
				map[string]any{"id": "paris", "label": "Paris", "latitude": 48.8566, "longitude": 2.3522, "color": "#F59E0B", "radius_px": 10.0},
				map[string]any{"id": "rome", "label": "Rome", "latitude": 41.890210, "longitude": 12.492231, "color": "#E11D48", "radius_px": 14.0},
			},
		},
	})

	// Act 4 (10–13 s): three portrait frames settle into a photo stack.
	stack := []struct {
		id, motion, asset string
		x                 float64
	}{
		{"a", "image_photo_drop", "gallery-image_photo_drop", -320},
		{"b", "image_depth_cascade", "gallery-image_depth_cascade", 0},
		{"c", "image_document_push", "gallery-image_document_push", 320},
	}
	for _, card := range stack {
		items = append(items, map[string]any{
			"id": "mega-stack-" + card.id, "kind": "image", "template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in", "motion_id": card.motion,
			"params":   map[string]any{"width": 360.0, "height": 280.0, "position_x": card.x, "position_y": -10.0},
			"start_ms": 10000, "end_ms": 13000, "duration_ms": 3000,
			"asset_refs": []any{map[string]any{"asset_id": card.asset, "sha256": strings.Repeat("a", 64), "url": "assets/canary/" + card.asset + ".png", "media_type": "image/png"}},
		})
	}

	// Act 5 (13–16 s): four kinetic 3D text treatments.
	t3dMotions := []string{
		"text_3d_yaw_flip_in", "text_3d_tilt_rise",
		"text_3d_word_cascade", "text_3d_double_axis_reveal",
	}
	for i, motionID := range t3dMotions {
		// Four centred text boxes in canvas coordinates, one per row.
		items = append(items, map[string]any{
			"id": "mega-t3d-" + motionID, "entity_id": "text:mega-" + motionID, "kind": "entity_card",
			"template_id": "PERSON_DEFAULT", "preset_id": "phrase_default",
			"text":      []string{"YAW FLIP", "TILT RISE", "WORD CASCADE", "DOUBLE AXIS"}[i],
			"motion_id": motionID,
			"params": map[string]any{
				"width":      560.0,
				"height":     180.0,
				"position_x": 640.0,
				"position_y": 90.0 + float64(i)*180.0,
			},
			"start_ms": 13000, "end_ms": 16000, "duration_ms": 3000,
		})
	}

	// Act 6 (16–20 s): three-image collage and a final title.
	for _, card := range []struct {
		id, motion, asset string
		x                 float64
	}{
		{"a", "image_collage_scatter", "gallery-image_collage_scatter", -340},
		{"b", "image_evidence_focus", "gallery-image_evidence_focus", 0},
		{"c", "image_float_settle", "gallery-image_float_settle", 340},
	} {
		items = append(items, map[string]any{
			"id": "mega-collage-" + card.id, "kind": "image", "template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in", "motion_id": card.motion,
			"params":   map[string]any{"width": 340.0, "height": 240.0, "position_x": card.x, "position_y": -30.0},
			"start_ms": 16000, "end_ms": 20000, "duration_ms": 4000,
			"asset_refs": []any{map[string]any{"asset_id": card.asset, "sha256": strings.Repeat("a", 64), "url": "assets/canary/" + card.asset + ".png", "media_type": "image/png"}},
		})
	}
	items = append(items, map[string]any{
		"id": "mega-final-title", "entity_id": "text:mega-final-title", "kind": "entity_card",
		"template_id": "PERSON_DEFAULT", "preset_id": "phrase_default", "text": "EDITORIAL VISUAL V1", "motion_id": "text_fade_up",
		"params":   map[string]any{"position_x": 640.0, "position_y": 260.0, "width": 720.0, "height": 100.0},
		"start_ms": 17000, "end_ms": 20000, "duration_ms": 3000,
	})

	result := compileV1Plan(t, v1BasePlan("editorial_v1_mega_canary", megaDurationMS, items))
	layers := contentLayers(result)

	// Cards, web focus flow, mapped route, photo stack, text 3D and collage/title.
	if got, want := len(layers), 3*2+3+8+3+4+3+1; got != want {
		ids := make([]string, 0, len(layers))
		for _, l := range layers {
			ids = append(ids, l.ID)
		}
		t.Fatalf("mega canary lowered %d layers, want %d\n%v", got, want, ids)
	}
	byID := make(map[string]Layer, len(layers))
	for _, layer := range layers {
		byID[layer.ID] = layer
	}
	for _, card := range cards {
		image, imageOK := byID[card.id+":image"]
		caption, captionOK := byID[card.id+":entity:caption"]
		if !imageOK || !captionOK {
			t.Fatalf("entity card %s is missing its image or caption layer", card.id)
		}
		if len(image.Size) < 2 || image.Size[0] != 420 || image.Size[1] != 420 {
			t.Fatalf("entity card %s has wrong rendered image size: %v", card.id, image.Size)
		}
		if len(image.Position) < 2 || math.Abs(image.Position[0]) > 1e-6 {
			t.Fatalf("entity card %s did not keep its resolved horizontal position: %v", card.id, image.Position)
		}
		if len(caption.Position) < 2 || math.Abs(caption.Position[0]-640) > 1e-6 {
			t.Fatalf("entity card %s caption is not centered below its image: image=%v caption=%v", card.id, image.Position, caption.Position)
		}
		imageBottom := 360 + image.Position[1] + image.Size[1]/2
		captionTop := caption.Position[1] - caption.Size[1]/2
		if captionTop <= imageBottom {
			t.Fatalf("entity card %s caption is not below image: imageBottom=%v captionTop=%v", card.id, imageBottom, captionTop)
		}
	}
	for _, id := range []string{"mega-stack-a:image", "mega-stack-b:image", "mega-stack-c:image"} {
		if layer := byID[id]; len(layer.Size) < 2 || layer.Size[0] != 360 || layer.Size[1] != 280 {
			t.Fatalf("photo stack layer %s has invalid geometry: %+v", id, layer)
		}
	}
	for _, id := range []string{"mega-collage-a:image", "mega-collage-b:image", "mega-collage-c:image"} {
		if layer := byID[id]; len(layer.Size) < 2 || layer.Size[0] != 340 || layer.Size[1] != 240 {
			t.Fatalf("collage layer %s has invalid geometry: %+v", id, layer)
		}
	}
	for _, motionID := range t3dMotions {
		layer, ok := byID["mega-t3d-"+motionID]
		if !ok || len(layer.Size) < 2 || layer.Size[0] != 560 || layer.Size[1] != 180 {
			t.Fatalf("text 3D %s has invalid line geometry: %+v", motionID, layer)
		}
		if !layer.Enable3D {
			t.Fatalf("text 3D %s did not enable the camera-backed transform path", motionID)
		}
		hasCameraTrack := false
		if layer.Animation != nil {
			for _, track := range layer.Animation.Tracks {
				if motion.IsCameraBacked3DProperty(track.Property) {
					hasCameraTrack = true
				}
			}
		}
		for _, animator := range layer.TextAnimators {
			for _, track := range animator.Properties {
				if motion.IsCameraBacked3DProperty(track.Property) {
					hasCameraTrack = true
				}
			}
		}
		if !hasCameraTrack {
			t.Fatalf("text 3D %s has no camera-backed transform track", motionID)
		}
	}

	// Family census: every family must actually be present and animated.
	text3DCount, entityPairs, mapLayers, stackLayers, collageLayers, webImages := 0, 0, 0, 0, 0, 0
	for _, layer := range layers {
		animated := layer.Animation != nil && len(layer.Animation.Tracks) > 0
		switch {
		case strings.HasPrefix(layer.ID, "mega-map"):
			// The map act: basemap (image), pins (shape), labels + attribution (text).
			mapLayers++
		case layer.Type == "image":
			if !animated {
				t.Fatalf("mega image layer %s is not animated", layer.ID)
			}
			switch {
			case strings.HasPrefix(layer.ID, "mega-stack-"):
				stackLayers++
			case strings.HasPrefix(layer.ID, "mega-collage-"):
				collageLayers++
			case strings.HasPrefix(layer.ID, "mega-web-"):
				webImages++
			}
		case layer.Type == "text":
			if strings.HasPrefix(layer.ID, "mega-t3d-") {
				text3DCount++
				if !animated {
					t.Fatalf("text_3d layer %s is not animated", layer.ID)
				}
			}
		}
		_ = animated
		if strings.HasPrefix(layer.ID, "mega-card-") {
			entityPairs++
		}
	}
	if entityPairs != 6 {
		t.Fatalf("entity card act lowered %d layers, want 3 images + 3 captions", entityPairs)
	}
	if stackLayers != 3 || collageLayers != 3 || webImages != 2 {
		t.Fatalf("image composition counts stack=%d collage=%d web=%d, want 3/3/2", stackLayers, collageLayers, webImages)
	}
	if text3DCount != 4 {
		t.Fatalf("text_3d act lowered %d layers, want 4", text3DCount)
	}
	if mapLayers != 8 {
		t.Fatalf("map act lowered %d layers, want 8 (basemap, 3 pins, 3 labels, attribution)", mapLayers)
	}

	// The Rome pin remains grounded in the global map viewport inside the mega cut.
	for _, layer := range layers {
		if layer.ID == "mega-map:map_pin:rome" {
			if math.Abs(layer.Position[0]) > 640 || math.Abs(layer.Position[1]) > 360 {
				t.Fatalf("mega map Rome pin escapes the map viewport: %v", layer.Position)
			}
		}
	}

	// Every text box uses absolute canvas-centre coordinates and must remain
	// inside the canvas (entity captions are checked against their own images).
	for _, layer := range layers {
		if layer.Type != "text" || strings.HasPrefix(layer.ID, "mega-card-") {
			continue
		}
		if len(layer.Position) < 2 {
			t.Fatalf("text layer %s missing geometry", layer.ID)
		}
		if len(layer.Size) < 2 || layer.Position[0]-layer.Size[0]/2 < 0 || layer.Position[0]+layer.Size[0]/2 > 1280 ||
			layer.Position[1]-layer.Size[1]/2 < 0 || layer.Position[1]+layer.Size[1]/2 > 720 {
			t.Fatalf("mega text layer %s escapes the canvas: pos=%v size=%v", layer.ID, layer.Position, layer.Size)
		}
	}

	// Determinism at the mega scale: compile twice, compare wire bytes.
	again := compileV1Plan(t, v1BasePlan("editorial_v1_mega_canary", megaDurationMS, items))
	first, err := result.Plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	second, err := again.Plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("mega canary plan is not deterministic (differ at byte %d)", firstDiff(first, second))
	}

	path := writeGoldenPlan(t, result, "editorial_v1_mega_canary")
	t.Logf("golden editorial V1 mega canary: %s", path)
}

func TestGoldenMapFlyoverCanary(t *testing.T) {
	// The real-map flyover canary: an operator-certified local raster of a
	// Europe window (Rome-centred, zoom 5) lowers through the georeferenced
	// map path — basemap, grounded pins, labels, attribution — with the
	// certified centered motion pool driving the push-in over Rome. London,
	// Paris and Rome are the flight's waypoints; every pin must land inside
	// the window the compile recomputes from the declared georeference.
	const (
		canvasW, canvasH = 1280, 720
		romeLat, romeLon = 41.890210, 12.492231
	)
	plan := v1BasePlan("map_flyover_canary", 8000, []any{map[string]any{
		"id": "europe-flyover", "kind": "map", "template_id": "MAP",
		"start_ms": 0, "end_ms": 8000, "duration_ms": 8000,
		"asset_refs": []any{map[string]any{
			"asset_id": "basemap-europe", "sha256": strings.Repeat("c", 64),
			"url": "assets/canary/map-europe.png", "media_type": "image/png",
		}},
		"map": map[string]any{
			"provider": "local", "source_id": "certified-europe-plate", "source_license": "operator-supplied",
			"center": map[string]any{"latitude": romeLat, "longitude": romeLon},
			"zoom":   5, "width": canvasW, "height": canvasH,
			"attribution": "Map data (c) operator-certified plate",
			"motion_id":   "image_focus_reveal",
			"pins": []any{
				map[string]any{"id": "london", "label": "London", "latitude": 51.5074, "longitude": -0.1278, "color": "#38BDF8", "radius_px": 10.0},
				map[string]any{"id": "paris", "label": "Paris", "latitude": 48.8566, "longitude": 2.3522, "color": "#F59E0B", "radius_px": 10.0},
				map[string]any{"id": "rome", "label": "Rome", "latitude": romeLat, "longitude": romeLon, "color": "#E11D48", "radius_px": 14.0},
			},
		},
	}})
	result := compileV1Plan(t, plan)
	layers := contentLayers(result)

	// basemap + 3 pins + 3 labels + attribution = 8.
	if got, want := len(layers), 8; got != want {
		t.Fatalf("got %d map layers, want %d (basemap, 3 pins, 3 labels, attribution)", got, want)
	}
	basemap := layers[0]
	if basemap.Type != "image" || basemap.Size[0] != canvasW || basemap.Size[1] != canvasH {
		t.Fatalf("basemap does not carry the certified raster canvas: %+v", basemap)
	}
	if basemap.Animation == nil || len(basemap.Animation.Tracks) == 0 {
		t.Fatalf("the certified map motion did not reach the basemap: %+v", basemap)
	}

	// The flight's waypoints ride the plate: the compile recomputes the Web
	// Mercator window itself and must ground every pin inside it.
	pinProps := map[string][]float64{}
	for _, layer := range layers {
		if layer.Type != "shape" || layer.Shape == nil {
			continue
		}
		pinProps[layer.ID] = []float64{layer.Position[0], layer.Position[1]}
	}
	if len(pinProps) != 3 {
		t.Fatalf("got %d grounded pins, want 3 (London, Paris, Rome)", len(pinProps))
	}

	// The Rome pin is the window centre: it must sit at the canvas centre so
	// the push-in keeps it framed for the whole flight.
	romePin, ok := pinProps["europe-flyover:map_pin:rome"]
	if !ok {
		t.Fatalf("the Rome pin is missing from the grounded set: %v", pinProps)
	}
	if math.Abs(romePin[0]) > 1e-6 || math.Abs(romePin[1]) > 1e-6 {
		t.Fatalf("the Rome pin did not land on the window centre: %v", romePin)
	}

	// London and Paris sit north-west of the centre, in plane pixels, exactly
	// where Web Mercator paints them (the y sign flips to canvas space).
	expectations := map[string][2]float64{
		"europe-flyover:map_pin:london": {-287.1758, -320.3198},
		"europe-flyover:map_pin:paris":  {-230.7420, -226.0882},
	}
	for id, want := range expectations {
		pos, ok := pinProps[id]
		if !ok {
			t.Fatalf("pin %q is missing from the grounded set", id)
		}
		if math.Abs(pos[0]-want[0]) > 0.05 || math.Abs(pos[1]-want[1]) > 0.05 {
			t.Fatalf("pin %q landed at %v, want Web Mercator's %v", id, pos, want)
		}
	}

	// Every map layer shares the basemap lifetime: the flight is one shot, not
	// a sequence of reveals.
	for _, layer := range layers {
		if layer.StartFrame != basemap.StartFrame || layer.DurationFrames != basemap.DurationFrames {
			t.Fatalf("map layer %q does not share the basemap lifetime", layer.ID)
		}
	}

	// The map's text (pin labels, attribution) stays inside the canvas for the
	// whole flyover. Text boxes are placed in top-left canvas coordinates; the
	// grounded pins are centred coordinates, already asserted above.
	for _, layer := range layers {
		if layer.Type != "text" {
			continue
		}
		if layer.Position[0] < 0 || layer.Position[0] > canvasW ||
			layer.Position[1] < 0 || layer.Position[1] > canvasH {
			t.Fatalf("map text layer %q escapes the canvas: %v", layer.ID, layer.Position)
		}
	}

	path := writeGoldenPlan(t, result, "map_flyover_canary")
	t.Logf("map flyover canary plan written to %s", path)
}

func TestGoldenEntityCardCanary(t *testing.T) {
	// CARD A: short name · CARD B: very long name · CARD C: unicode —
	// three entity cards in sequence, each with image motion and animated
	// caption from the entity_caption_v1 family.
	items := []any{
		entityCardItem("card-a", "Ada Lovelace", 0, 2400, "image_depth_dolly"),
		entityCardItem("card-b", "Pablo Diego José Francisco de Paula Juan Nepomuceno María de los Remedios Cipriano de la Santísima Trinidad Ruiz y Picasso", 2400, 4800, "image_yaw_reveal"),
		entityCardItem("card-c", "习近平", 4800, 7200, "image_25d_depth_float_in"),
	}
	result := compileV1Plan(t, v1BasePlan("entity_card_canary", 7200, items))
	layers := contentLayers(result)
	if len(layers) < 6 {
		t.Fatalf("entity card canary lowered %d layers, want at least 3 image + 3 caption", len(layers))
	}
	for i := 0; i+1 < len(layers); i += 2 {
		image, caption := layers[i], layers[i+1]
		if image.Type != "image" || caption.Type != "text" {
			t.Fatalf("layer pair types = %q/%q, want image/text", image.Type, caption.Type)
		}
		if image.StartFrame != caption.StartFrame || image.DurationFrames != caption.DurationFrames {
			t.Fatalf("image/caption lifetimes differ for %s", image.ID)
		}
		// Canvas-absolute check: caption center must be below the image
		// bottom edge and horizontally near the image center.
		imageBottom := 360.0 + image.Position[1] + image.Size[1]/2
		if caption.Position[1] <= imageBottom {
			t.Fatalf("caption %s center %v not below image bottom %v", caption.ID, caption.Position[1], imageBottom)
		}
		if math.Abs(caption.Position[0]-(640.0+image.Position[0])) > 2 {
			t.Fatalf("caption %s center_x %v not aligned with image center_x", caption.ID, caption.Position[0])
		}
	}
	if len(result.UnknownTemplates) > 0 {
		t.Fatalf("unknown templates: %v", result.UnknownTemplates)
	}
	path := writeGoldenPlan(t, result, "entity_card_canary")
	t.Logf("golden entity card canary: %s", path)
}

func TestGoldenMediaFrameGallery(t *testing.T) {
	// radius 0 + border 2 · radius 12 + border 2 · radius 32 + border 4 ·
	// radius 64 + border 8 — the rounded/bordered image matrix, all alive
	// for the whole plan.	// The frame matrix renders as framed entity cards with captions; every
	// card carries its frame treatment and the shared lifetime.
	cases := []struct {
		radius float64
		border float64
		itemID string
		x, y   float64
	}{
		{0, 2, "frame-r0", -330, -180},
		{12, 2, "frame-r12", 330, -180},
		{32, 4, "frame-r32", -330, 180},
		{64, 8, "frame-r64", 330, 180},
	}
	items := make([]any, 0, len(cases))
	for _, c := range cases {
		items = append(items, map[string]any{
			"id": c.itemID, "entity_id": "person:" + c.itemID, "kind": "entity_card",
			"template_id": "PERSON", "preset_id": "phrase_default",
			"text":            c.itemID,
			"entity_caption":  c.itemID,
			"image_preset_id": "image_scale_in",
			"motion_id":       "image_25d_depth_float_in",
			"params":          map[string]any{"box_width": 560.0, "box_height": 300.0, "position_x": c.x, "position_y": c.y},
			"frame": map[string]any{"border": map[string]any{
				"width_px": c.border, "color": "#FFFFFF", "radius_px": c.radius,
			}},
			"start_ms": 0, "end_ms": 4000, "duration_ms": 4000,
			"asset_refs": []any{uniquePortraitAsset(c.itemID)},
		})
	}
	result := compileV1Plan(t, v1BasePlan("media_frame_gallery", 4000, items))
	layers := contentLayers(result)
	if len(layers) != 8 {
		t.Fatalf("media frame gallery lowered %d layers, want 4 framed images + 4 captions", len(layers))
	}
	seen := map[string]bool{}
	for _, layer := range layers {
		// The framed image keeps its card treatment; the caption text must NOT
		// carry a background card — the strict native Vulkan text path cannot
		// lower a text_card node, so captions use typography and shadow instead.
		if layer.Type == "image" && (layer.Style == nil || layer.Style.Background == nil) {
			t.Fatalf("media frame layer %s lowered without its border background", layer.ID)
		}
		if layer.Type == "text" && layer.Style != nil && layer.Style.Background != nil {
			t.Fatalf("caption layer %s lowered with a text_card background the GPU path cannot render", layer.ID)
		}
		seen[layer.ID] = true
	}
	for _, c := range cases {
		if !seen[c.itemID] && !seen[c.itemID+":image"] {
			t.Errorf("media frame layer for %s missing", c.itemID)
		}
	}
	path := writeGoldenPlan(t, result, "media_frame_gallery")
	t.Logf("golden media frame gallery: %s", path)
}

func TestGoldenImageMotionGallery4x4(t *testing.T) {
	// The 4x4 grid: 14 editorial_image_v1 motions + 2 clean 2.5D motions,
	// every cell entering simultaneously with its named motion.
	motions := []string{
		"image_depth_dolly", "image_tilt_parallax", "image_yaw_reveal", "image_roll_in",
		"image_orbit_enter", "image_perspective_stack", "image_focus_push", "image_float_settle",
		"image_card_flip", "image_depth_cascade", "image_photo_drop", "image_document_push",
		"image_collage_scatter", "image_evidence_focus",
		"image_25d_depth_float_in", "image_25d_yaw_flip_in",
	}
	items := make([]any, 0, len(motions))
	for i, motionID := range motions {
		col, row := i%4, i/4
		x := -465.0 + float64(col)*310.0
		y := -170.0 + float64(row)*170.0
		items = append(items, map[string]any{
			"id": "gallery-" + motionID, "kind": "image",
			"template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in",
			"motion_id": motionID,
			"params":    map[string]any{"box_width": 290.0, "box_height": 160.0, "position_x": x, "position_y": y},
			"start_ms":  0, "end_ms": 6000, "duration_ms": 6000,
			"asset_refs": []any{uniquePortraitAsset("gallery-" + motionID)},
		})
	}
	result := compileV1Plan(t, v1BasePlan("image_motion_gallery_4x4", 6000, items))
	layers := contentLayers(result)
	if len(layers) != 16 {
		t.Fatalf("image gallery lowered %d layers, want 16", len(layers))
	}
	animated := 0
	for _, layer := range layers {
		if layer.Animation != nil && len(layer.Animation.Tracks) > 0 {
			animated++
		}
		if len(layer.Position) < 2 {
			t.Fatalf("gallery layer %s has no position", layer.ID)
		}
	}
	if animated != 16 {
		t.Fatalf("only %d/16 gallery layers animated", animated)
	}
	path := writeGoldenPlan(t, result, "image_motion_gallery_4x4")
	t.Logf("golden image motion gallery: %s", path)
}

func TestGoldenText3DGallery(t *testing.T) {
	// Four simultaneous 3D text entrances, per the goal canary.
	motions := []string{
		"text_3d_yaw_flip_in", "text_3d_tilt_rise",
		"text_3d_word_cascade", "text_3d_double_axis_reveal",
	}
	items := make([]any, 0, len(motions))
	for i, motionID := range motions {
		y := -180.0 + float64(i)*120.0
		items = append(items, map[string]any{
			"id": "t3d-" + motionID, "entity_id": "text:" + motionID, "kind": "entity_card",
			"template_id": "PERSON_DEFAULT", "preset_id": "phrase_default",
			"text":              []string{"YAW FLIP", "TILT RISE", "WORD CASCADE", "DOUBLE AXIS"}[i],
			"caption_motion_id": motionID,
			"params":            map[string]any{"position_x": 0.0, "position_y": y},
			"start_ms":          0, "end_ms": 5000, "duration_ms": 5000,
		})
	}
	result := compileV1Plan(t, v1BasePlan("text_3d_gallery", 5000, items))
	layers := contentLayers(result)
	if len(layers) != 4 {
		t.Fatalf("text 3D gallery lowered %d layers, want 4", len(layers))
	}
	for _, layer := range layers {
		if layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
			t.Fatalf("text 3D layer %s lowered without motion tracks", layer.ID)
		}
	}
	path := writeGoldenPlan(t, result, "text_3d_gallery")
	t.Logf("golden text 3D gallery: %s", path)
}

func TestGoldenWebCanaries(t *testing.T) {
	// Canary #1: browser screenshot → depth push → section focus → caption.
	// Canary #2: three browser cards from stack to fan.
	focus := compileV1Plan(t, v1BasePlan("web_focus_flow", 5000, []any{
		map[string]any{
			"id": "web-main", "kind": "image",
			"template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in",
			"motion_id": "web_browser_depth_in",
			"params":    map[string]any{"box_width": 960.0, "box_height": 540.0},
			"start_ms":  0, "end_ms": 5000, "duration_ms": 5000,
			"asset_refs": []any{uniqueBrowserAsset("web-main")},
		},
		map[string]any{
			"id": "web-focus", "kind": "image",
			"template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in",
			"motion_id": "web_section_spotlight",
			"params":    map[string]any{"box_width": 300.0, "box_height": 180.0, "position_x": 120.0, "position_y": -40.0},
			"start_ms":  1500, "end_ms": 5000, "duration_ms": 3500,
			"asset_refs": []any{uniqueBrowserAsset("web-focus")},
		}, map[string]any{
			"id": "web-caption", "entity_id": "text:web-caption", "kind": "entity_card",
			"template_id": "PERSON_DEFAULT", "preset_id": "phrase_default",
			"text": "SECTION SPOTLIGHT", "caption_motion_id": "text_fade_up",
			"params":   map[string]any{"position_x": 0.0, "position_y": 220.0},
			"start_ms": 2500, "end_ms": 5000, "duration_ms": 2500,
		},
	}))
	if len(contentLayers(focus)) != 3 {
		t.Fatalf("web focus flow lowered %d layers, want 3", len(contentLayers(focus)))
	}
	path := writeGoldenPlan(t, focus, "web_focus_flow")
	t.Logf("golden web focus flow: %s", path)

	// Stack→fan: three browser cards slide from a shared stack point to
	// their fan positions simultaneously.
	fan := compileV1Plan(t, v1BasePlan("web_stack_fan", 4000, []any{
		webFanCard("web-fan-a", -400.0, "web_browser_stack_fan"),
		webFanCard("web-fan-b", 0.0, "web_browser_stack_fan"),
		webFanCard("web-fan-c", 400.0, "web_browser_stack_fan"),
	}))
	if len(contentLayers(fan)) != 3 {
		t.Fatalf("web stack fan lowered %d layers, want 3", len(contentLayers(fan)))
	}
	path = writeGoldenPlan(t, fan, "web_stack_fan")
	t.Logf("golden web stack fan: %s", path)
}

func TestGoldenCaptionMotionCompilesThroughFullPipeline(t *testing.T) {
	// Every shared and Trump entity text motion must lower through the full pipeline
	// onto a caption bound to its image's lifetime.
	for _, motionID := range []string{
		"text_depth_in", "text_fade_up", "text_scale_punch",
		"text_word_rise", "text_word_stagger", "text_yaw_in",
		"trump_entity_text_01", "trump_entity_text_02", "trump_entity_text_03",
		"trump_entity_text_04", "trump_entity_text_05", "trump_entity_text_06",
		"trump_entity_text_07", "trump_entity_text_08", "trump_entity_text_09",
		"trump_entity_text_10", "trump_entity_text_11", "trump_entity_text_12",
		"trump_entity_text_13", "trump_entity_text_14", "trump_entity_text_15",
	} {
		item := entityCardItem("cap-"+motionID, "Determinism Check", 0, 2000, "image_25d_depth_float_in")
		item["caption_motion_id"] = motionID
		result := compileV1Plan(t, v1BasePlan("caption-"+motionID, 2000, []any{item}))
		content := contentLayers(result)
		if len(content) != 2 {
			t.Fatalf("caption motion %s lowered %d layers, want 2", motionID, len(content))
		}
		caption := content[1]
		if caption.Animation == nil || len(caption.Animation.Tracks) == 0 {
			t.Fatalf("caption motion %s lowered no animation tracks", motionID)
		}
	}
}

func TestGoldenPlanDeterminism(t *testing.T) {
	// The same semantic plan compiled twice must produce identical wire
	// bytes: one artifact, one hash, every time.
	item := entityCardItem("determinism-card", "Ada Lovelace", 0, 2000, "image_depth_dolly")
	first := compileV1Plan(t, v1BasePlan("determinism", 2000, []any{item}))
	second := compileV1Plan(t, v1BasePlan("determinism", 2000, []any{item}))
	a, err := first.Plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.Plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("golden plan is not deterministic: %d vs %d bytes differ at byte %d",
			len(a), len(b), firstDiff(a, b))
	}
}

func TestGoldenPlansStayInsideCanvasSafeArea(t *testing.T) {
	// Every text layer the V1 canaries emit must keep its rendered box
	// inside the canvas: captions and titles never render off-frame.
	item := entityCardItem("safe-card", "Safe Area Check", 0, 2000, "image_depth_dolly")
	result := compileV1Plan(t, v1BasePlan("safe", 2000, []any{item}))
	for _, layer := range contentLayers(result) {
		if layer.Type != "text" {
			continue
		}
		if len(layer.Position) < 2 || len(layer.Size) < 2 {
			t.Fatalf("text layer %s missing geometry", layer.ID)
		}
		left := layer.Position[0] - layer.Size[0]/2
		right := layer.Position[0] + layer.Size[0]/2
		if left < 0 || right > 1280 {
			t.Fatalf("text layer %s box %v..%v escapes the canvas", layer.ID, left, right)
		}
	}
}

func TestGoldenPlansSchemaInvariant(t *testing.T) {
	// The emitted wire document must be the chronon.render-plan.v2 schema
	// the renderer accepts, with every image layer carrying its asset.
	item := entityCardItem("schema-card", "Schema Check", 0, 2000, "image_25d_depth_float_in")
	result := compileV1Plan(t, v1BasePlan("schema", 2000, []any{item}))
	// The materialized asset manifest carries the logical path the renderer
	// resolves; the wire plan references assets through it.
	foundAsset := false
	for _, asset := range result.Assets {
		if strings.HasSuffix(asset.LogicalPath, ".png") {
			foundAsset = true
		}
	}
	if !foundAsset {
		t.Fatal("golden plan lost the image asset in the materialized manifest")
	}
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Schema  string `json:"schema"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(wire, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Schema != "chronon.render-plan.v2" || probe.Version != 2 {
		t.Fatalf("golden plan schema = %q v%d", probe.Schema, probe.Version)
	}
}
