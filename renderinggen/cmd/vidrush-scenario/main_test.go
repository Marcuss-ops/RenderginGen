package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// scenarioResult is what the compile-level gates read: the compiled plan, the
// descriptor the render script drives, and the raw semantic bytes.
type scenarioResult struct {
	plan       *overlay.Plan
	descriptor scenarioDescriptor
	semantic   []byte
	render     []byte
}

func buildScenario(t *testing.T) scenarioResult {
	t.Helper()
	assetsRoot := t.TempDir()
	outDir := t.TempDir()
	if err := run(assetsRoot, outDir, ""); err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	semantic, err := os.ReadFile(filepath.Join(outDir, "semantic_plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := overlay.CompileSemantic(semantic)
	if err != nil {
		t.Fatalf("compile semantic plan: %v", err)
	}
	if len(result.UnknownTemplates) > 0 {
		t.Fatalf("scenario names unknown templates: %v", result.UnknownTemplates)
	}
	render, err := os.ReadFile(filepath.Join(outDir, "render_plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor scenarioDescriptor
	data, err := os.ReadFile(filepath.Join(outDir, "scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &descriptor); err != nil {
		t.Fatalf("decode descriptor: %v", err)
	}
	return scenarioResult{plan: result.Plan, descriptor: descriptor, semantic: semantic, render: render}
}

func layersByID(plan *overlay.Plan) map[string]overlay.Layer {
	byID := make(map[string]overlay.Layer, len(plan.Layers))
	for _, layer := range plan.Layers {
		byID[layer.ID] = layer
	}
	return byID
}

// animated reports whether a layer really carries an entrance animation: a
// layer track or a text animator. A grounded pin or an attribution label is
// deliberately static and is asserted separately.
func animated(layer overlay.Layer) bool {
	if layer.Animation != nil && len(layer.Animation.Tracks) > 0 {
		return true
	}
	return len(layer.TextAnimators) > 0
}

// TestVidrushScenarioLayersAreWired is the structural gate: every act of the
// concatenated scenario lowers to the layers its family promises, and every
// animated family really carries motion on the compiled plan.
func TestVidrushScenarioLayersAreWired(t *testing.T) {
	result := buildScenario(t)
	byID := layersByID(result.plan)

	// ── Act: frasi — two animated phrases ────────────────────────────────
	for _, id := range []string{"frasi", "frasi_b"} {
		layer, ok := byID[id]
		if !ok || layer.Type != "text" {
			t.Fatalf("phrase %s did not lower to a text layer: %+v", id, layer)
		}
		if !animated(layer) {
			t.Fatalf("phrase %s lowered without motion", id)
		}
	}

	// ── Act: date & metric — both presentation families animated ─────────
	for _, id := range []string{"data", "metrica"} {
		layer, ok := byID[id]
		if !ok || layer.Type != "text" {
			t.Fatalf("%s did not lower to a text layer: %+v", id, layer)
		}
		if !animated(layer) {
			t.Fatalf("%s lowered without its presentation motion", id)
		}
	}

	// ── Act: entità — exactly two entity cards, both with text ───────────
	person, ok := byID["entita_persona:image"]
	if !ok || person.Type != "image" {
		t.Fatalf("entity portrait did not lower to an image layer: %+v", person)
	}
	if !animated(person) {
		t.Fatal("entity portrait lowered without motion")
	}
	caption, ok := byID["entita_persona:entity:caption"]
	if !ok || caption.Type != "text" {
		t.Fatalf("entity caption did not lower to a text layer: %+v", caption)
	}
	if !animated(caption) {
		t.Fatal("entity caption lowered without motion")
	}
	if len(caption.Position) < 2 || len(person.Position) < 2 {
		t.Fatal("entity card layers carry no geometry")
	}
	// The caption is anchored below the image: image bottom edge + margin.
	imageBottom := float64(canvasHeight)/2 + person.Position[1] + person.Size[1]/2
	if caption.Position[1]-caption.Size[1]/2 <= imageBottom {
		t.Fatalf("entity caption top %v is not below the image bottom %v", caption.Position[1]-caption.Size[1]/2, imageBottom)
	}
	org, ok := byID["entita_organizzazione"]
	if !ok || org.Type != "text" || !animated(org) {
		t.Fatalf("organization card did not lower to an animated text layer: %+v", org)
	}
	entityCards := 0
	for id := range byID {
		if id == "entita_persona:image" || id == "entita_organizzazione" {
			entityCards++
		}
	}
	if entityCards != 2 {
		t.Fatalf("scenario carries %d entity cards, want the max-2 scene", entityCards)
	}

	// ── Act: luoghi — a location card and the grounded map ───────────────
	location, ok := byID["luogo"]
	if !ok || location.Type != "text" || !animated(location) {
		t.Fatalf("location card did not lower to an animated text layer: %+v", location)
	}
	basemap, ok := byID["mappa:map_basemap"]
	if !ok || basemap.Type != "image" || !animated(basemap) {
		t.Fatalf("map basemap did not lower to an animated image layer: %+v", basemap)
	}
	if len(basemap.Size) < 2 || basemap.Size[0] != canvasWidth || basemap.Size[1] != canvasHeight {
		t.Fatalf("map basemap geometry %v does not match the %dx%d canvas", basemap.Size, canvasWidth, canvasHeight)
	}
	for _, pin := range mapPins {
		marker, ok := byID["mappa:map_pin:"+pin.ID]
		if !ok || marker.Type != "shape" {
			t.Fatalf("map pin %s did not lower to a shape layer: %+v", pin.ID, marker)
		}
		label, ok := byID["mappa:map_pin_label:"+pin.ID]
		if !ok || label.Type != "text" {
			t.Fatalf("map pin label %s did not lower to a text layer: %+v", pin.ID, label)
		}
		// The compile recomputes the Web Mercator window itself; every pin it
		// admits must land inside the canvas.
		if len(marker.Position) < 2 ||
			marker.Position[0] < -float64(canvasWidth)/2 || marker.Position[0] > float64(canvasWidth)/2 ||
			marker.Position[1] < -float64(canvasHeight)/2 || marker.Position[1] > float64(canvasHeight)/2 {
			t.Fatalf("map pin %s landed outside the canvas: %v", pin.ID, marker.Position)
		}
	}
	attribution, ok := byID["mappa:map_attribution"]
	if !ok || attribution.Type != "text" {
		t.Fatalf("map attribution is missing: %+v", attribution)
	}

	// ── Act: immagini — the x2/x3/x4/x5 composite matrix ─────────────────
	for _, tc := range []struct {
		item  string
		count int
	}{
		{"immagini_x2", 2}, {"immagini_x3", 3}, {"immagini_x4", 4}, {"immagini_x5", 5},
	} {
		found := 0
		for id, layer := range byID {
			if !strings.HasPrefix(id, tc.item+":") || !strings.HasSuffix(id, ":image") {
				continue
			}
			found++
			if layer.Type != "image" || !animated(layer) {
				t.Fatalf("%s child %s did not lower to an animated image layer: %+v", tc.item, id, layer)
			}
			if len(layer.Position) < 2 || len(layer.Size) < 2 {
				t.Fatalf("%s child %s carries no geometry", tc.item, id)
			}
		}
		if found != tc.count {
			t.Fatalf("%s lowered %d animated children, want %d", tc.item, found, tc.count)
		}
	}

	// ── Layer census: no family may silently drop ─────────────────────────
	if got, want := len(result.plan.Layers), 1+2+2+2+1+1+8+14; got != want {
		ids := make([]string, 0, len(result.plan.Layers))
		for _, layer := range result.plan.Layers {
			ids = append(ids, layer.ID)
		}
		t.Fatalf("compiled %d layers, want %d (background, frasi, date+metric, 2 entity cards, location, 8 map layers, 14 image children)\n%v",
			got, want, ids)
	}
}

// TestVidrushScenarioTimelineIsConcatenated pins the "animazioni concat"
// contract: acts are contiguous — every act starts on the frame the previous
// one ends, the first act starts at zero and the last ends at the declared
// duration — and every item sits inside its act.
func TestVidrushScenarioTimelineIsConcatenated(t *testing.T) {
	result := buildScenario(t)
	if len(result.descriptor.Acts) == 0 {
		t.Fatal("descriptor declares no acts")
	}
	cursor := int64(0)
	itemCount := 0
	for index, act := range result.descriptor.Acts {
		if act.Start != cursor {
			t.Fatalf("act %d (%s) starts at %d, want %d: the concatenation has a gap or an overlap", index, act.ID, act.Start, cursor)
		}
		if act.End <= act.Start {
			t.Fatalf("act %d (%s) has an empty window [%d,%d)", index, act.ID, act.Start, act.End)
		}
		for _, item := range act.Items {
			itemCount++
			if item.StartMS < act.Start || item.EndMS > act.End {
				t.Fatalf("item %s [%d,%d) escapes its act [%d,%d)", item.Name, item.StartMS, item.EndMS, act.Start, act.End)
			}
			if item.StartFrame != msToFrame(item.StartMS) || item.MidFrame <= item.StartFrame || item.EntryFrame <= item.StartFrame {
				t.Fatalf("item %s has invalid probe frames: %+v", item.Name, item)
			}
			if item.MinDiffPixels <= 0 {
				t.Fatalf("item %s has no presence threshold", item.Name)
			}
		}
		cursor = act.End
	}
	if cursor != result.descriptor.DurationMS {
		t.Fatalf("acts end at %d, want the declared duration %d", cursor, result.descriptor.DurationMS)
	}
	if itemCount != len(scenarioItems) {
		t.Fatalf("descriptor covers %d items, want %d", itemCount, len(scenarioItems))
	}
	// Every frame of the timeline belongs to exactly one act, so the render is
	// one continuous chain of animations.
	if got := result.plan.Canvas.DurationFrames; got != int64(scenarioDuration)*scenarioFPS/1000 {
		t.Fatalf("compiled duration %d frames, want %d", got, int64(scenarioDuration)*scenarioFPS/1000)
	}
}

// TestVidrushScenarioIsDeterministic compiles the same scenario twice and
// requires byte-identical plans: one artifact, one hash, every run.
func TestVidrushScenarioIsDeterministic(t *testing.T) {
	first := buildScenario(t)
	second := buildScenario(t)
	if string(first.semantic) != string(second.semantic) {
		t.Fatal("semantic plan is not deterministic")
	}
	if string(first.render) != string(second.render) {
		t.Fatal("compiled render plan is not deterministic")
	}
}
