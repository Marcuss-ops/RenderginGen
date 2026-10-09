package overlay

import (
	"strconv"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func TestPhraseHighlightCatalogMotionsCompileWithAllAccents(t *testing.T) {
	ids := motion.Registry.CategoryMotionIDs("phrase_highlight_v1")
	if len(ids) != 16 {
		t.Fatalf("phrase highlight catalog has %d IDs, want 16", len(ids))
	}
	multiAccentMotions := 0
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			plugin, err := motion.Registry.Resolve(id)
			if err != nil {
				t.Fatal(err)
			}
			definition := plugin.(motion.DeclarativePlugin).Definition
			if len(definition.LayerComponents) == 0 {
				t.Fatal("phrase highlight motion has no authored accent components")
			}
			if len(definition.LayerComponents) > 1 {
				multiAccentMotions++
			}
			componentIDs := make(map[string]struct{}, len(definition.LayerComponents))
			for _, component := range definition.LayerComponents {
				if component.ID == "" {
					t.Fatalf("motion %q has an accent with an empty id", id)
				}
				if _, duplicate := componentIDs[component.ID]; duplicate {
					t.Fatalf("motion %q repeats accent id %q", id, component.ID)
				}
				componentIDs[component.ID] = struct{}{}
				if component.Stroke != nil {
					t.Fatalf("accent %q must use fill-only native geometry for Vulkan admission, got stroke %+v", component.ID, component.Stroke)
				}
			}
			for _, component := range definition.LayerComponents {
				if component.AbsolutePosition || len(component.PositionScale) != 2 {
					t.Fatalf("accent %q must use normalized canvas-relative positioning, got absolute=%t position_scale=%v", component.ID, component.AbsolutePosition, component.PositionScale)
				}
			}
			for _, canvas := range [][2]int{{1920, 1080}, {1280, 720}} {
				raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"highlight-` + id + `","video_id":"highlight-test","width":` + strconv.Itoa(canvas[0]) + `,"height":` + strconv.Itoa(canvas[1]) + `,"fps_num":30,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"` + id + `","text":"THE AUTHORED HIGHLIGHT PHRASE","start_ms":0,"end_ms":5000}]}`)
				result, err := CompileSemantic(raw)
				if err != nil {
					t.Fatalf("compile highlight motion: %v", err)
				}
				if len(result.Plan.Layers) != len(definition.LayerComponents)+1 {
					t.Fatalf("compiled %d layers, want %d accents + text", len(result.Plan.Layers), len(definition.LayerComponents)+1)
				}
				for index, component := range definition.LayerComponents {
					layer := result.Plan.Layers[index]
					if layer.ID != "phrase:accent:"+component.ID {
						t.Fatalf("accent layer %d id=%q, want phrase:accent:%s (catalog order must be preserved)", index, layer.ID, component.ID)
					}
					if layer.Type != "shape" || layer.Shape == nil || layer.Shape.Type != "rect" || layer.Shape.Fill == nil || layer.Shape.Stroke != nil {
						t.Fatalf("accent %q did not lower to a filled rounded-rectangle GPU shape: %+v", component.ID, layer)
					}
					if len(component.Fill) != 4 || component.Opacity == nil || *component.Opacity <= 0 || *component.Opacity > 1 {
						t.Fatalf("accent %q has invalid fill or authored opacity: fill=%v opacity=%v", component.ID, component.Fill, component.Opacity)
					}
					wantOpacity := *component.Opacity
					if layer.Opacity == nil || *layer.Opacity != wantOpacity {
						t.Fatalf("accent %q compiled opacity=%v, want authored opacity %v", component.ID, layer.Opacity, wantOpacity)
					}
					compiledFill, ok := layer.Shape.Fill.([]float64)
					if !ok || len(compiledFill) != 4 {
						t.Fatalf("accent %q compiled fill has unexpected representation: %#v", component.ID, layer.Shape.Fill)
					}
					for channel, value := range component.Fill {
						if compiledFill[channel] != value {
							t.Fatalf("accent %q compiled fill=%v, want %v", component.ID, layer.Shape.Fill, component.Fill)
						}
					}
					if layer.Animation == nil || len(layer.Animation.Tracks) != len(component.Tracks) {
						t.Fatalf("accent %q track count does not match authored recipe", component.ID)
					}
					for trackIndex, authored := range component.Tracks {
						got := layer.Animation.Tracks[trackIndex]
						if got.Component != component.ID || got.Property != authored.Property || len(got.Keyframes) < len(authored.Keyframes) {
							t.Fatalf("accent %q track %d routing/property/keyframe count=%+v, want component=%q property=%q authored keys=%d", component.ID, trackIndex, got, component.ID, authored.Property, len(authored.Keyframes))
						}
						keyIndex := 0
						for _, authoredKey := range authored.Keyframes {
							found := false
							for keyIndex < len(got.Keyframes) {
								compiledKey := got.Keyframes[keyIndex]
								keyIndex++
								if compiledKey.Value == authoredKey.Value {
									found = true
									break
								}
							}
							if !found {
								t.Fatalf("accent %q track %q lost or reordered authored keyframe value %v", component.ID, authored.Property, authoredKey.Value)
							}
						}
					}

					wantWidth := float64(canvas[0]) * component.SizeScale[0]
					wantHeight := float64(canvas[1]) * component.SizeScale[1]
					if len(layer.Size) != 2 || layer.Size[0] != wantWidth || layer.Size[1] != wantHeight {
						t.Fatalf("accent %q size=%v, want (%v,%v) at canvas %v", component.ID, layer.Size, wantWidth, wantHeight, canvas)
					}
					wantX := float64(canvas[0]) * component.PositionScale[0]
					wantY := float64(canvas[1]) * component.PositionScale[1]
					if layer.Position[0] != wantX || layer.Position[1] != wantY {
						t.Fatalf("accent %q position=%v, want (%v,%v) at canvas %v", component.ID, layer.Position, wantX, wantY, canvas)
					}
				}
				text := result.Plan.Layers[len(result.Plan.Layers)-1]
				if text.Type != "text" || text.Animation == nil || len(text.Animation.Tracks) == 0 {
					t.Fatalf("phrase entrance missing from final text layer: %+v", text)
				}
				if _, err := result.Plan.Marshal(); err != nil {
					t.Fatalf("marshal compiled highlight plan: %v", err)
				}
			}
		})
	}
	if multiAccentMotions != 5 {
		t.Fatalf("phrase highlight catalog has %d multi-accent motions, want 5", multiAccentMotions)
	}
}
