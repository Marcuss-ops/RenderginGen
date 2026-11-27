// composition_check.go compares the composition REQUIREMENT the worker derives
// from the plan (planHasVisualOverlay) with the execution path Chronon actually
// reports in its bounded telemetry summary.
//
// The worker's prediction is a requirement fact, not a physical choice: it says
// "this plan has an authored overlay, so a single decoded source cannot feed the
// encoder". Before this check nothing verified that the two agreed, which left
// two silent failure modes:
//
//   - the plan HAS an authored overlay and the engine took the single-source
//     path anyway. The render cannot be what the plan asked for. This direction
//     fails closed at every policy: the artifact is not publishable.
//   - the plan has NO authored overlay and the engine composited regardless.
//     Nothing is wrong with the picture, but the worker's requirement fact was
//     over-permissive and the cost was paid invisibly. Recorded always; fails
//     closed only under the certify policy, which claims proof of contract.
//
// Absence of the summary is not a violation: the summary is an optional sidecar
// (the rendered bytes are valid without it), so "cannot check" is recorded as
// its own metric rather than reported as agreement. That is the same fail-open,
// never-silent contract the telemetry ingest already follows.
package processor

import (
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// compositionDivergence classifies the disagreement between the prediction and
// the report. The empty value means they agree (or the report said nothing).
type compositionDivergence string

const (
	// divergenceNone: the engine's report is consistent with the prediction, or
	// the document did not report enough to tell.
	divergenceNone compositionDivergence = ""
	// divergenceSingleSourceDespiteOverlay: the plan declares an authored
	// overlay but the engine used the single-source path.
	divergenceSingleSourceDespiteOverlay compositionDivergence = "single_source_path_despite_overlay"
	// divergenceCompositedUnpredicted: the engine composited even though the
	// worker predicted it did not need to.
	divergenceCompositedUnpredicted compositionDivergence = "composited_despite_no_predicted_overlay"
)

// checkCompositionPrediction classifies one report against one prediction.
//
// predicted is planHasVisualOverlay's answer for the compiled plan; facts is the
// engine's report. An unreported execution path is only a divergence when the
// composite counters ALSO say nothing — the counters are the independent
// witness, so a document that omits the path but shows composite frames still
// catches the over-permissive direction.
func checkCompositionPrediction(predicted bool, facts chronon.ExecutionFacts) compositionDivergence {
	composited, known := facts.Composited()
	if predicted {
		// An authored overlay cannot be served by the single-source path.
		if facts.DirectSource() {
			return divergenceSingleSourceDespiteOverlay
		}
		return divergenceNone
	}
	if known && composited {
		return divergenceCompositedUnpredicted
	}
	return divergenceNone
}

// verifyCompositionPrediction runs the cross-check for a finalized artifact and
// records the outcome on the artifact metrics.
//
// It returns an error for the direction that can produce a wrong picture, and
// under certify/strict also when an authored overlay cannot be verified at all.
// Those two levels claim proof of the composition contract, so an empty or
// undecodable summary must not be silently certified: the artifact is rejected
// with the reason instead. fast/normal/sample remain fail-open on an absent
// optional sidecar (the rendered bytes are still valid), because they never
// claim that proof. A plan with no authored overlay has no composition
// requirement to verify, so absence stays fail-open even under certify/strict.
func verifyCompositionPrediction(plan *overlay.Plan, raw json.RawMessage, metrics map[string]float64, level renderVerifyLevel, jobID string) error {
	predicted := planHasVisualOverlay(plan)
	unverifiable := func(reason string) error {
		metrics[metricnames.CompositionPredictionUnverifiable] = 1
		if predicted && (level == renderVerifyCertify || level == renderVerifyStrict) {
			return fmt.Errorf("processor: composition prediction unverifiable under policy %s: the plan declares an authored overlay but %s", level, reason)
		}
		workerlog.ByJobID(jobID).Warnf("composition prediction not verifiable (fail-open by design under policy=%s): %s", level, reason)
		return nil
	}
	if len(raw) == 0 {
		return unverifiable("the execution summary is empty")
	}
	facts, err := chronon.DecodeExecutionFacts(raw)
	if err != nil {
		return unverifiable(fmt.Sprintf("the execution summary could not be decoded: %v", err))
	}
	divergence := checkCompositionPrediction(predicted, facts)
	if divergence == divergenceNone {
		return nil
	}
	metrics[metricnames.CompositionPredictionDivergence] = 1
	composited, known := facts.Composited()
	workerlog.ByJobID(jobID).Errorf("composition prediction divergence %s: predicted_overlay=%t execution_path=%s composited=%v (reported=%t) handoff=%s",
		divergence, predicted, facts.ExecutionPath(), composited, known, facts.Job.SurfaceHandoffPath)
	switch divergence {
	case divergenceSingleSourceDespiteOverlay:
		return fmt.Errorf("processor: composition prediction divergence %s: the plan declares an authored overlay but Chronon reported execution_path=%s, so the single decoded source did not carry the plan's layers",
			divergence, facts.ExecutionPath())
	case divergenceCompositedUnpredicted:
		if level == renderVerifyCertify || level == renderVerifyStrict {
			return fmt.Errorf("processor: composition prediction divergence %s under policy %s: Chronon reported composite frames for a plan the worker classified as needing no compositor",
				divergence, level)
		}
	}
	return nil
}
