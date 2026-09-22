// execution_facts.go owns the bounded summary slice that says how the engine
// ACTUALLY executed a job.
//
// Why it exists. The worker sends Chronon a REQUIREMENT ("this plan has an
// authored overlay, so a single decoded source cannot feed the encoder") derived
// from its own layer count (planHasVisualOverlay), and then had no way to learn
// whether the engine agreed: the two decisions were never compared, so a worker
// prediction that stopped matching the engine's selection would be discovered
// only by an operator looking at a wrong picture. Chronon's bounded telemetry
// summary already records the physical decision — execution_path,
// surface_handoff_path and the composite-frame counters — so the prediction can
// be checked against the report instead of trusted.
//
// This is a decode of the SAME versioned documents as NativeTelemetry — the
// historical bounded summary (chronon3d.render-telemetry-summary.v1) and the
// current engine's v2 frame-timing sidecar (chronon3d.frame-timing.v2), whose
// bounded `summary` + `job` sections carry the same field paths inline. It is
// kept as its own type because the two answer different questions:
// NativeTelemetry certifies the strict-native receipt contract, this says which
// execution path was taken. Neither is the raw deep-profile sidecar.
package chronon

import (
	"encoding/json"
	"fmt"
)

// Execution path values the engine reports. Only the two this client reasons
// about are named; an unknown value is reported as-is and treated as "not
// DirectYUV" by callers that must distinguish them.
const (
	// ExecutionPathDirectYUV is the single-source path: one decoded source
	// straight to the encoder, with no compositor in the loop.
	ExecutionPathDirectYUV = "direct_yuv"
	// ExecutionPathFullGraphNative is the composited graph path.
	ExecutionPathFullGraphNative = "full_graph_native"
)

// ExecutionFacts is how the engine says it executed the job.
//
// The counters are POINTERS so a missing counter is distinguishable from a
// measured zero, matching NativeTelemetry: an absent composite counter means
// "the engine did not report it", which is not the same as "no compositing
// happened".
type ExecutionFacts struct {
	Schema string `json:"schema"`
	Job    struct {
		ExecutionPath      string `json:"execution_path"`
		SurfaceHandoffPath string `json:"surface_handoff_path"`
		GPU                struct {
			// CompositeFrames counts frames the engine composited on the GPU.
			CompositeFrames *int64 `json:"cuda_composite_frames"`
			// CompositeBlendMS is the graph's blend time; it is a second,
			// independent witness that compositing work happened.
			CompositeBlendMS *float64 `json:"compositenode_blend_ms"`
		} `json:"gpu"`
	} `json:"job"`
}

// DecodeExecutionFacts decodes a bounded telemetry document for the
// execution-path cross-check. It accepts exactly the two documented telemetry
// shapes and rejects everything else, so the cross-check can never be fed the
// raw deep-profile sidecar or a future unversioned shape:
//
//   - chronon3d.render-telemetry-summary.v1 — the historical bounded summary,
//     decoded as-is.
//   - chronon3d.frame-timing.v2 — the CURRENT engine's ONLY telemetry artifact.
//     It carries the same bounded sections (`summary` + `job`, the same field
//     paths) INLINE next to an unbounded per-frame array, so it is bounded by
//     BoundTimingSidecar — the same projection the ledger ingest uses — before
//     decoding. Accepting it is load-bearing: the legacy summary is forbidden
//     by Chronon3d's architecture contract, so gating on the v1 schema alone
//     made every clip report CompositionPredictionUnverifiable=1 and skipped the
//     check entirely (the exact silent-non-verification this file exists to
//     prevent).
func DecodeExecutionFacts(raw json.RawMessage) (ExecutionFacts, error) {
	var facts ExecutionFacts
	if len(raw) == 0 {
		return facts, fmt.Errorf("chronon execution facts: empty document")
	}
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return facts, fmt.Errorf("chronon execution facts: decode schema: %w", err)
	}
	document := raw
	switch header.Schema {
	case TelemetrySummarySchema:
		// Decode the bounded summary verbatim.
	case TimingSidecarSchemaV2:
		bounded, err := BoundTimingSidecar(raw)
		if err != nil {
			return facts, fmt.Errorf("chronon execution facts: %w", err)
		}
		document = bounded
	default:
		return facts, fmt.Errorf("chronon execution facts: schema %q, want %q or %q",
			header.Schema, TelemetrySummarySchema, TimingSidecarSchemaV2)
	}
	if err := json.Unmarshal(document, &facts); err != nil {
		return facts, fmt.Errorf("chronon execution facts: decode: %w", err)
	}
	return facts, nil
}

// DirectSource reports whether the engine took the single-source path, which is
// the path an authored overlay cannot use.
func (f ExecutionFacts) DirectSource() bool {
	return f.Job.ExecutionPath == ExecutionPathDirectYUV
}

// Composited reports whether the engine's own counters show compositing work,
// and whether the document said anything about it at all.
//
// known=false means neither composite counter was present, so the cross-check
// cannot conclude anything: reporting that as "no compositing" would turn a
// missing counter into a confident (and possibly wrong) verification.
func (f ExecutionFacts) Composited() (composited, known bool) {
	if f.Job.GPU.CompositeFrames != nil {
		return *f.Job.GPU.CompositeFrames > 0, true
	}
	if f.Job.GPU.CompositeBlendMS != nil {
		return *f.Job.GPU.CompositeBlendMS > 0, true
	}
	return false, false
}

// ExecutionPath reports the engine's execution path, or "unreported" when the
// document omitted it (an older summary, or a field rename).
func (f ExecutionFacts) ExecutionPath() string {
	if f.Job.ExecutionPath == "" {
		return "unreported"
	}
	return f.Job.ExecutionPath
}
