package chronon

import (
	"encoding/json"
	"testing"
)

// summaryDoc builds a bounded telemetry summary with the given job facts. The
// shape mirrors the engine's own document (see the recorded summaries under the
// Chronon checkout), and is deliberately minimal: only the fields
// ExecutionFacts reads.
func summaryDoc(t *testing.T, job map[string]any) json.RawMessage {
	t.Helper()
	doc := map[string]any{"schema": TelemetrySummarySchema, "job": job}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodeExecutionFactsRejectsNonSummaryDocuments(t *testing.T) {
	cases := map[string][]byte{
		"empty":                   nil,
		"malformed":               []byte("{"),
		"unversioned":             []byte(`{"schema":"chronon3d.render-telemetry.v1","job":{}}`),
		"raw timing":              []byte(`{"frame_times_ms":[1,2,3]}`),
		"summary of another repo": []byte(`{"schema":"renderinggen.overlay-plan.v1"}`),
	}
	for name, raw := range cases {
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeExecutionFacts(raw); err == nil {
				t.Fatalf("%s must be rejected: the cross-check may only read the versioned summary", name)
			}
		})
	}
}

func TestExecutionFactsDirectSource(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{ExecutionPathDirectYUV, true},
		{ExecutionPathFullGraphNative, false},
		{"", false},
		{"some_future_path", false},
	}
	for _, tc := range cases {
		raw := summaryDoc(t, map[string]any{"execution_path": tc.path})
		facts, err := DecodeExecutionFacts(raw)
		if err != nil {
			t.Fatalf("decode %q: %v", tc.path, err)
		}
		if got := facts.DirectSource(); got != tc.want {
			t.Errorf("DirectSource() for execution_path=%q = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestExecutionFactsComposited pins the three-state answer: composited, not
// composited, and "the document did not say". Collapsing the third into the
// second would let a missing counter masquerade as a verified prediction.
func TestExecutionFactsComposited(t *testing.T) {
	cases := []struct {
		name           string
		gpu            map[string]any
		wantComposited bool
		wantKnown      bool
	}{
		{"frames present and positive", map[string]any{"cuda_composite_frames": 30}, true, true},
		{"frames present and zero", map[string]any{"cuda_composite_frames": 0}, false, true},
		{"frames absent, blend present and positive", map[string]any{"compositenode_blend_ms": 1.5}, true, true},
		{"frames absent, blend present and zero", map[string]any{"compositenode_blend_ms": 0}, false, true},
		{"neither counter present", map[string]any{"other": 1}, false, false},
		{"no gpu block at all", nil, false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			job := map[string]any{}
			if tc.gpu != nil {
				job["gpu"] = tc.gpu
			}
			facts, err := DecodeExecutionFacts(summaryDoc(t, job))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			composited, known := facts.Composited()
			if composited != tc.wantComposited || known != tc.wantKnown {
				t.Fatalf("Composited() = (%v, %v), want (%v, %v)", composited, known, tc.wantComposited, tc.wantKnown)
			}
		})
	}
}

// TestExecutionFactsExecutionPathNamesTheAbsence pins that an omitted path is
// reported as such rather than as an empty string that reads like a value.
func TestExecutionFactsExecutionPathNamesTheAbsence(t *testing.T) {
	facts, err := DecodeExecutionFacts(summaryDoc(t, map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := facts.ExecutionPath(); got != "unreported" {
		t.Fatalf("ExecutionPath() = %q, want \"unreported\" for an omitted field", got)
	}
	facts, err = DecodeExecutionFacts(summaryDoc(t, map[string]any{"execution_path": ExecutionPathFullGraphNative}))
	if err != nil {
		t.Fatal(err)
	}
	if got := facts.ExecutionPath(); got != ExecutionPathFullGraphNative {
		t.Fatalf("ExecutionPath() = %q, want %q", got, ExecutionPathFullGraphNative)
	}
}

// TestDecodeExecutionFactsAcceptsARecordedSummaryField-set pins the decoder
// against the engine's documented field names: these are the keys the recorded
// summaries carry, so a rename on either side fails here.
func TestDecodeExecutionFactsReadsTheEngineFieldNames(t *testing.T) {
	raw := json.RawMessage(`{"schema":"` + TelemetrySummarySchema + `","job":{"execution_path":"full_graph_native","surface_handoff_path":"direct","gpu":{"cuda_composite_frames":120}}}`)
	facts, err := DecodeExecutionFacts(raw)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Job.SurfaceHandoffPath != "direct" {
		t.Errorf("surface_handoff_path = %q, want direct", facts.Job.SurfaceHandoffPath)
	}
	composited, known := facts.Composited()
	if !known || !composited {
		t.Errorf("Composited() = (%v, %v), want (true, true)", composited, known)
	}
	if facts.Job.GPU.CompositeFrames == nil || *facts.Job.GPU.CompositeFrames != 120 {
		t.Errorf("cuda_composite_frames = %v, want 120", facts.Job.GPU.CompositeFrames)
	}
}
