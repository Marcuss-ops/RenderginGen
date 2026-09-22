package processor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// compositionSummary builds a bounded telemetry summary carrying just the
// execution facts the cross-check reads.
func compositionSummary(t *testing.T, executionPath string, compositeFrames *int64) json.RawMessage {
	t.Helper()
	gpu := map[string]any{}
	if compositeFrames != nil {
		gpu["cuda_composite_frames"] = *compositeFrames
	}
	raw, err := json.Marshal(map[string]any{
		"schema": chronon.TelemetrySummarySchema,
		"job": map[string]any{
			"execution_path":       executionPath,
			"surface_handoff_path": "direct",
			"gpu":                  gpu,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func int64Ptr(v int64) *int64 { return &v }

// overlayPlan is a plan with an authored overlay: a text layer, which no single
// decoded source can produce.
func overlayPlan() *overlay.Plan {
	return &overlay.Plan{Layers: []overlay.Layer{
		{ID: "source", Type: "video", Source: "assets/semantic/a.mp4"},
		{ID: "title", Type: "text"},
	}}
}

// plainPlan is a single-source plan: no authored overlay.
func plainPlan() *overlay.Plan {
	return &overlay.Plan{Layers: []overlay.Layer{
		{ID: "source", Type: "video", Source: "assets/semantic/a.mp4"},
	}}
}

func factsFor(t *testing.T, raw json.RawMessage) chronon.ExecutionFacts {
	t.Helper()
	facts, err := chronon.DecodeExecutionFacts(raw)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

// TestCheckCompositionPrediction pins the classification, including the case the
// whole check exists for: an authored overlay rendered through the single-source
// path.
func TestCheckCompositionPrediction(t *testing.T) {
	cases := []struct {
		name      string
		predicted bool
		path      string
		frames    *int64
		want      compositionDivergence
	}{
		{
			name:      "overlay predicted, engine composited",
			predicted: true,
			path:      chronon.ExecutionPathFullGraphNative,
			frames:    int64Ptr(120),
			want:      divergenceNone,
		},
		{
			name:      "overlay predicted, engine took the single-source path",
			predicted: true,
			path:      chronon.ExecutionPathDirectYUV,
			frames:    int64Ptr(0),
			want:      divergenceSingleSourceDespiteOverlay,
		},
		{
			name:      "no overlay predicted, engine composited anyway",
			predicted: false,
			path:      chronon.ExecutionPathFullGraphNative,
			frames:    int64Ptr(30),
			want:      divergenceCompositedUnpredicted,
		},
		{
			name:      "no overlay predicted, engine took the single-source path",
			predicted: false,
			path:      chronon.ExecutionPathDirectYUV,
			frames:    int64Ptr(0),
			want:      divergenceNone,
		},
		{
			name:      "no overlay predicted, counters absent",
			predicted: false,
			path:      chronon.ExecutionPathFullGraphNative,
			frames:    nil,
			want:      divergenceNone,
		},
		{
			name:      "overlay predicted, counters absent but path reported",
			predicted: true,
			path:      chronon.ExecutionPathFullGraphNative,
			frames:    nil,
			want:      divergenceNone,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			facts := factsFor(t, compositionSummary(t, tc.path, tc.frames))
			if got := checkCompositionPrediction(tc.predicted, facts); got != tc.want {
				t.Fatalf("checkCompositionPrediction(%v, %+v) = %q, want %q", tc.predicted, facts.Job, got, tc.want)
			}
		})
	}
}

// TestVerifyCompositionPredictionSingleSourceAlwaysFailsClosed pins the
// dangerous direction at EVERY policy: an authored overlay rendered through the
// single-source path is not the picture the plan asked for, so the artifact is
// not publishable even under the permissive default.
func TestVerifyCompositionPredictionSingleSourceAlwaysFailsClosed(t *testing.T) {
	for _, level := range []renderVerifyLevel{renderVerifyFast, renderVerifyNormal, renderVerifyCertify} {
		level := level
		t.Run(string(level), func(t *testing.T) {
			metrics := map[string]float64{}
			raw := compositionSummary(t, chronon.ExecutionPathDirectYUV, int64Ptr(0))
			err := verifyCompositionPrediction(overlayPlan(), raw, metrics, level, "job-1")
			if err == nil {
				t.Fatalf("policy %s must reject an overlay rendered through the single-source path", level)
			}
			if !strings.Contains(err.Error(), string(divergenceSingleSourceDespiteOverlay)) {
				t.Errorf("error = %q, want it to name the divergence", err)
			}
			if !strings.Contains(err.Error(), chronon.ExecutionPathDirectYUV) {
				t.Errorf("error = %q, want it to name the engine's execution path", err)
			}
			if metrics[metricnames.CompositionPredictionDivergence] != 1 {
				t.Errorf("metrics = %v, want %s=1", metrics, metricnames.CompositionPredictionDivergence)
			}
		})
	}
}

// TestVerifyCompositionPredictionUnpredictedCompositeDependsOnPolicy pins the
// documented asymmetry: a render that composited anyway is still a valid
// picture, so it is recorded everywhere and rejected only where the worker
// claims proof of contract.
func TestVerifyCompositionPredictionUnpredictedCompositeDependsOnPolicy(t *testing.T) {
	raw := compositionSummary(t, chronon.ExecutionPathFullGraphNative, int64Ptr(30))

	for _, level := range []renderVerifyLevel{renderVerifyFast, renderVerifyNormal} {
		metrics := map[string]float64{}
		if err := verifyCompositionPrediction(plainPlan(), raw, metrics, level, "job-1"); err != nil {
			t.Fatalf("policy %s must accept a valid render whose requirement fact was over-permissive: %v", level, err)
		}
		if metrics[metricnames.CompositionPredictionDivergence] != 1 {
			t.Errorf("policy %s: metrics = %v, want the divergence recorded", level, metrics)
		}
	}

	metrics := map[string]float64{}
	err := verifyCompositionPrediction(plainPlan(), raw, metrics, renderVerifyCertify, "job-1")
	if err == nil {
		t.Fatal("certify claims proof of contract, so an unexplained composite pass must fail closed")
	}
	if !strings.Contains(err.Error(), string(divergenceCompositedUnpredicted)) {
		t.Errorf("error = %q, want it to name the divergence", err)
	}
}

// TestVerifyCompositionPredictionAgreementIsSilent pins that a plan whose
// prediction matched the report leaves no metric and no error: the cross-check
// must not manufacture noise on the happy path.
func TestVerifyCompositionPredictionAgreementIsSilent(t *testing.T) {
	cases := []struct {
		name string
		plan *overlay.Plan
		raw  json.RawMessage
	}{
		{"overlay composited", overlayPlan(), compositionSummary(t, chronon.ExecutionPathFullGraphNative, int64Ptr(120))},
		{"single source without overlay", plainPlan(), compositionSummary(t, chronon.ExecutionPathDirectYUV, int64Ptr(0))},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			metrics := map[string]float64{}
			if err := verifyCompositionPrediction(tc.plan, tc.raw, metrics, renderVerifyCertify, "job-1"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(metrics) != 0 {
				t.Fatalf("metrics = %v, want none on agreement", metrics)
			}
		})
	}
}

// TestVerifyCompositionPredictionUnverifiableIsRecordedNotFatal pins the
// fail-open half: the summary is an optional sidecar, so an absent or
// undecodable one is recorded as unverifiable and never fails a valid artifact.
func TestVerifyCompositionPredictionUnverifiableIsRecordedNotFatal(t *testing.T) {
	cases := map[string]json.RawMessage{
		"absent":       nil,
		"empty":        {},
		"malformed":    json.RawMessage("{"),
		"wrong schema": json.RawMessage(`{"schema":"chronon3d.some-other.v1","job":{}}`),
	}
	for name, raw := range cases {
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			for _, level := range []renderVerifyLevel{renderVerifyFast, renderVerifyCertify} {
				metrics := map[string]float64{}
				if err := verifyCompositionPrediction(overlayPlan(), raw, metrics, level, "job-1"); err != nil {
					t.Fatalf("policy %s: an optional sidecar must not fail the artifact: %v", level, err)
				}
				if metrics[metricnames.CompositionPredictionUnverifiable] != 1 {
					t.Errorf("policy %s: metrics = %v, want %s=1", level, metrics, metricnames.CompositionPredictionUnverifiable)
				}
				if metrics[metricnames.CompositionPredictionDivergence] != 0 {
					t.Errorf("policy %s: an unverifiable check must not report a divergence: %v", level, metrics)
				}
			}
		})
	}
}

// TestVerifyCompositionPredictionRunsOnTheV2TimingSidecar pins the end-to-end
// consequence of the decoder fix (P1, Sept 2026): the current engine's ONLY
// telemetry artifact is the v2 frame-timing sidecar, so the cross-check must
// actually RUN on it. Before the fix every clip recorded
// CompositionPredictionUnverifiable=1 and the comparison was skipped in
// silence — the exact failure the check exists to prevent.
func TestVerifyCompositionPredictionRunsOnTheV2TimingSidecar(t *testing.T) {
	v2 := func(executionPath string, compositeFrames int64) json.RawMessage {
		raw, err := json.Marshal(map[string]any{
			"schema":  chronon.TimingSidecarSchemaV2,
			"version": 2,
			"job": map[string]any{
				"execution_path": executionPath,
				"gpu":            map[string]any{"cuda_composite_frames": compositeFrames},
			},
			"summary":        map[string]any{"render_only_fps": 24.0},
			"frame_times_ms": []float64{1.0, 1.1, 1.2},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	// The direction that can produce a wrong picture must be CAUGHT, and the
	// check must not degrade to "unverifiable".
	metrics := map[string]float64{}
	err := verifyCompositionPrediction(overlayPlan(), v2(chronon.ExecutionPathDirectYUV, 0), metrics, renderVerifyFast, "job-1")
	if err == nil {
		t.Fatal("a v2 sidecar reporting the single-source path for an overlay plan must fail closed")
	}
	if metrics[metricnames.CompositionPredictionUnverifiable] != 0 {
		t.Errorf("metrics = %v: a v2 sidecar is verifiable, so it must not be recorded as unverifiable", metrics)
	}
	if metrics[metricnames.CompositionPredictionDivergence] != 1 {
		t.Errorf("metrics = %v, want %s=1", metrics, metricnames.CompositionPredictionDivergence)
	}

	// Agreement stays silent on the current engine's artifact too.
	metrics = map[string]float64{}
	if err := verifyCompositionPrediction(overlayPlan(), v2(chronon.ExecutionPathFullGraphNative, 120), metrics, renderVerifyCertify, "job-1"); err != nil {
		t.Fatalf("unexpected error on agreement: %v", err)
	}
	if len(metrics) != 0 {
		t.Fatalf("metrics = %v, want none on agreement", metrics)
	}
}

// TestCrossCheckedMetricNamesAreDeclared pins that the two counters reach the
// ledger vocabulary. An undeclared metric name is dropped by the queue's
// projection, which would make the counters above invisible in production.
func TestCrossCheckedMetricNamesAreDeclared(t *testing.T) {
	for _, name := range []string{
		metricnames.CompositionPredictionDivergence,
		metricnames.CompositionPredictionUnverifiable,
	} {
		if _, ok := metricnames.Unit(name); !ok {
			t.Errorf("metric %q is not declared in the metric vocabulary", name)
		}
	}
}
