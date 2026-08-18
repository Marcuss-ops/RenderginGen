package chronon

import (
	"encoding/json"
	"fmt"
	"os"
)

// ReadTimingSidecar reads the frame-timing sidecar Chronon writes next to the
// rendered output (`<output>.timing.json`, emitted by the video pipe exporter
// without requiring --report) and returns it as a JSON document for the
// worker's artifact ledger.
//
// Chronon is the source of truth for plan/graph/GPU/encoder timing, so the
// worker ingests the document verbatim (Chronon owns the schema) and only
// records its own distributive phases (materialize, sha256, uploads, total)
// separately. The unbounded per-frame `frame_times_ms` array is dropped here:
// it is deep-profiling detail that stays in the sidecar file itself, keeping
// the ledger row bounded regardless of frame count.
func ReadTimingSidecar(outputPath string) (json.RawMessage, error) {
	data, err := os.ReadFile(outputPath + ".timing.json")
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: decode: %w", err)
	}
	delete(doc, "frame_times_ms")
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: re-encode: %w", err)
	}
	return out, nil
}
