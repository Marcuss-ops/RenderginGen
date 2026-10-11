package overlay

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/contractschema"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/schemaval"
)

// localOverlaySchemaPath is the repo-local overlay-plan.v1 schema. Unlike
// contractPresetPath it needs no go.work workspace, so these producer→worker
// boundary tests actually run in a standalone RenderingGen checkout instead of
// skipping.
func localOverlaySchemaPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(contractschema.Dir(t), overlayContractSchema)
}

// pipelineGenChainPlan is the VERBATIM bytes produced by PipelineGen's
// CoalesceChainedOverlays + OverlayPlan marshal for two nearby phrases fused
// into one phrase_chain item (animation_counts: {chain_gap_ms: 600}). Every
// field — item id, kind, template_id, window, chain_segments, chain_camera —
// is the producer's real output, so this is the cross-repo half of the
// Multi-Animations Runtime contract: the opt-in chain path is not "verified
// only up to the layer" in a hand-written fixture, its producer bytes are
// accepted by THIS compiler.
const pipelineGenChainPlan = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-boundary","video_id":"video-chain","width":1280,"height":720,"fps_num":24,"fps_den":1,"duration_ms":4000,"items":[{"id":"scene-1:chain-text:0","scene_id":"scene-1","kind":"phrase_chain","start_ms":0,"end_ms":4000,"duration_us":4000000,"template_id":"PHRASE_CHAIN","preset_id":"phrase_default","motion_id":"phrase_apple_clean_07_slide_up_soft","text":"prima frase","params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_gap_us":400000,"chain_motions":["phrase_apple_clean_07_slide_up_soft","soft_edge_spotlight_dissolve"],"chain_segments":[{"text":"prima frase","motion_id":"phrase_apple_clean_07_slide_up_soft","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":0,"duration_us":1800000},{"text":"seconda frase","motion_id":"soft_edge_spotlight_dissolve","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":2200000,"duration_us":1800000}]},"duration_ms":4000}]}`

// TestPipelineGenChainPlanLowersEverySegment feeds the producer's real chain
// payload through the one compiler and pins that both segments lower inside the
// same pass, the first with its own motion and the second with the fly-under
// dip + fade handoff — i.e. the chain is renderable, not merely wire-valid.
func TestPipelineGenChainPlanLowersEverySegment(t *testing.T) {
	if err := schemaval.ValidateFile([]byte(pipelineGenChainPlan), localOverlaySchemaPath(t)); err != nil {
		t.Fatalf("PipelineGen chain plan is not valid overlay-plan.v1: %v", err)
	}
	result, err := CompileSemantic([]byte(pipelineGenChainPlan))
	if err != nil {
		t.Fatalf("PipelineGen chain plan must compile: %v", err)
	}
	if len(result.UnknownTemplates) != 0 {
		t.Fatalf("PHRASE_CHAIN must be a registered template, got unknown: %v", result.UnknownTemplates)
	}
	var first, second *Layer
	for i := range result.Plan.Layers {
		if strings.Contains(result.Plan.Layers[i].ID, "seg0") && first == nil {
			first = &result.Plan.Layers[i]
		}
		if strings.Contains(result.Plan.Layers[i].ID, "seg1") && second == nil {
			second = &result.Plan.Layers[i]
		}
	}
	if first == nil || second == nil {
		t.Fatalf("chain segments must lower to layers, got %v", layerIDs(result.Plan.Layers))
	}
	if first.Text != "prima frase" || second.Text != "seconda frase" {
		t.Fatalf("chain segment texts = %q / %q", first.Text, second.Text)
	}
	if first.TransitionIn != nil {
		t.Fatalf("first chain segment must enter with its own motion, got %+v", first.TransitionIn)
	}
	if second.TransitionIn == nil || second.TransitionIn.Kind != "fly_under_fade" {
		t.Fatalf("non-first chain segment must carry the fly_under_fade handoff, got %+v", second.TransitionIn)
	}
	if second.StartFrame <= first.StartFrame {
		t.Fatalf("chain segments must be ordered in time: %d vs %d", first.StartFrame, second.StartFrame)
	}
}

// pipelineGenChartPlan is the VERBATIM bytes produced by PipelineGen's
// BuildChartItems for a grounded "42% vs 18%" comparison, lowered to a text
// label plus native rectangular SHAPE bars (shape.chart is deliberately not
// emitted). It is the cross-repo half of the chart contract: the producer's
// real comparison payload must reach a renderable Chronon plan.
const pipelineGenChartPlan = `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chart-boundary","video_id":"video-chart","width":1280,"height":720,"fps_num":24,"fps_den":1,"duration_ms":2000,"items":[{"id":"scene-1-chart-42--vs-18","scene_id":"scene-1","kind":"text_phrase","start_ms":0,"end_ms":2000,"duration_us":2000000,"template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"42%\n18%","params":{"chart_card":true,"chart_group_id":"scene-1-chart-42--vs-18","fill":"#FFFFFF","font_size_px":30.240000000000002,"height":302.4,"position":"center","position_x":400.384,"position_y":360,"style":"headline","width":405.50399999999996},"duration_ms":2000},{"id":"scene-1-chart-42--vs-18__bar_00","scene_id":"scene-1","kind":"shape","start_ms":0,"end_ms":2000,"duration_us":2000000,"template_id":"SHAPE","params":{"chart_bar":true,"chart_group_id":"scene-1-chart-42--vs-18","fill":[0.2,0.78,0.94,1],"height":78.624,"position":[861.184,284.4],"shape":"rect","width":405.50399999999996},"duration_ms":2000},{"id":"scene-1-chart-42--vs-18__bar_01","scene_id":"scene-1","kind":"shape","start_ms":0,"end_ms":2000,"duration_us":2000000,"template_id":"SHAPE","params":{"chart_bar":true,"chart_group_id":"scene-1-chart-42--vs-18","fill":[0.2,0.78,0.94,1],"height":78.624,"position":[745.3257142857143,435.6],"shape":"rect","width":173.78742857142853},"duration_ms":2000}]}`

// TestPipelineGenChartPlanLowersToRectAndLabel pins that the producer's chart
// payload compiles to the structural primitives the worker actually renders —
// one text label card plus two rectangular bars — and that no layer carries the
// rejected shape.chart payload.
func TestPipelineGenChartPlanLowersToRectAndLabel(t *testing.T) {
	// The producer payload must itself be a valid overlay-plan.v1 document.
	if err := schemaval.ValidateFile([]byte(pipelineGenChartPlan), localOverlaySchemaPath(t)); err != nil {
		t.Fatalf("PipelineGen chart plan is not valid overlay-plan.v1: %v", err)
	}
	result, err := CompileSemantic([]byte(pipelineGenChartPlan))
	if err != nil {
		t.Fatalf("PipelineGen chart plan must compile: %v", err)
	}
	var label *Layer
	var rects int
	for i := range result.Plan.Layers {
		switch {
		case strings.Contains(result.Plan.Layers[i].ID, "42--vs-18") && strings.Contains(result.Plan.Layers[i].ID, "bar"):
			rects++
			if result.Plan.Layers[i].Shape == nil || result.Plan.Layers[i].Shape.Type != "rect" {
				t.Fatalf("chart bar %q must lower to a rect shape, got %+v", result.Plan.Layers[i].ID, result.Plan.Layers[i].Shape)
			}
		case result.Plan.Layers[i].ID == "scene-1-chart-42--vs-18":
			label = &result.Plan.Layers[i]
		}
	}
	if rects != 2 {
		t.Fatalf("chart must lower to two rectangular bars, got %d (%v)", rects, layerIDs(result.Plan.Layers))
	}
	if label == nil {
		t.Fatalf("chart must lower its label card, got %v", layerIDs(result.Plan.Layers))
	}
	if label.Text != "42%\n18%" {
		t.Fatalf("chart label text = %q, want the grounded %q", label.Text, "42%\n18%")
	}
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal Chronon render plan: %v", err)
	}
	if strings.Contains(string(wire), `"chart"`) {
		t.Fatalf("compiled plan must not carry a shape.chart payload: %s", wire)
	}
}
