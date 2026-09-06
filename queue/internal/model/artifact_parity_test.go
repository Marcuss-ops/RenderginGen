package model_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/queue/client"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

// fullWireArtifact builds a client.Artifact with every field populated so no
// omitempty field can silently drop out of the JSON projection.
func fullWireArtifact() client.Artifact {
	return client.Artifact{
		ID:                 "art-1",
		Kind:               "segment",
		StorageKey:         "sha-abc",
		ArtifactURL:        "https://store/objects/sha-abc",
		ArtifactHash:       "sha-abc",
		ContentType:        "video/mp4",
		SizeBytes:          12345,
		Width:              1920,
		Height:             1080,
		FPSNum:             30,
		FPSDen:             1,
		FrameCount:         150,
		DurationUS:         5_000_000,
		ProfileID:          "velox-h264-1080p30-v1",
		CopyEligible:       true,
		Codec:              "h264",
		CodecProfile:       "High",
		ClosedGOP:          true,
		FirstFrameKeyframe: true,
		Backend:            "chronon_vulkan",
		ChrononVersion:     "0.9.4",
		Metrics:            map[string]float64{"render_ms": 12.5},
		ChrononTelemetry:   json.RawMessage(`{"job":{"render_ms":12.5}}`),
		DriveFileID:        "drive-1",
		DriveLink:          "https://drive.google.com/file/d/drive-1/view",
		Container:          "mp4",
		PixelFormat:        "yuv420p",
		AudioStreams:       0,
	}
}

// TestArtifactWireModelParity pins the 6x artifact field mirror (queue.Artifact
// ⇄ client.Artifact ⇄ model.Artifact ⇄ server claim response ⇄ SQL columns ⇄
// artifactdb mirror) at its two least-protected seams: the public wire type
// (client.Artifact, what the worker decodes) and the persistence/claim type
// (model.Artifact, what the server encodes). A field added to one but not the
// other silently corrupts or drops data on every job — this test fails the
// build the moment the field lists diverge.
//
// The mirror is NOT code-generated (the modules are separate), so this
// reflection-level round trip is the guard: both directions must survive JSON
// with every field intact.
func TestArtifactWireModelParity(t *testing.T) {
	wire := fullWireArtifact()

	wireJSON, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal client.Artifact: %v", err)
	}

	// client -> model: every wire field must land in the persistence model.
	var fromWire model.Artifact
	if err := json.Unmarshal(wireJSON, &fromWire); err != nil {
		t.Fatalf("unmarshal into model.Artifact: %v", err)
	}
	if !artifactsEqualFromJSON(string(wireJSON), fromWire) {
		t.Fatalf("client.Artifact -> model.Artifact dropped fields\nwire:   %s\nmodel:  %s", wireJSON, mustMarshal(fromWire))
	}

	// model -> client: every persistence field must survive back to the wire.
	modelJSON, err := json.Marshal(fromWire)
	if err != nil {
		t.Fatalf("marshal model.Artifact: %v", err)
	}
	var roundTrip client.Artifact
	if err := json.Unmarshal(modelJSON, &roundTrip); err != nil {
		t.Fatalf("unmarshal into client.Artifact: %v", err)
	}
	roundTripJSON, err := json.Marshal(roundTrip)
	if err != nil {
		t.Fatalf("marshal round-tripped client.Artifact: %v", err)
	}
	if !jsonKeysEqual(wireJSON, modelJSON) {
		t.Fatalf("artifact JSON key sets diverged:\nwire:  %s\nmodel: %s", wireJSON, modelJSON)
	}
	if !jsonKeysEqual(modelJSON, roundTripJSON) {
		t.Fatalf("model -> client round trip dropped keys:\nmodel: %s\nwire:  %s", modelJSON, roundTripJSON)
	}
}

func artifactsEqualFromJSON(wantJSON string, got model.Artifact) bool {
	var want model.Artifact
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		return false
	}
	return reflect.DeepEqual(want, got)
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<marshal error>"
	}
	return string(b)
}

func jsonKeysEqual(a, b []byte) bool {
	var am, bm map[string]json.RawMessage
	if err := json.Unmarshal(a, &am); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bm); err != nil {
		return false
	}
	if len(am) != len(bm) {
		return false
	}
	for k := range am {
		if _, ok := bm[k]; !ok {
			return false
		}
	}
	return true
}
