package processor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// contractPath resolves a checked-in contract from the repository root.
// `go test` runs with the package directory as CWD, so the root is three
// levels up from renderinggen/internal/processor.
func contractPath(name string) string {
	return filepath.Join("..", "..", "..", "contracts", name)
}

// TestOverlayPrepareContractFileMatchesValidator pins the overlay.prepare
// boundary to a checked-in contract. This is the one job type the worker
// accepts that is NOT a Chronon plan and NOT the semantic overlay plan, and it
// used to exist only as a Go constant with no contract document — so the wire
// shape was whatever the validator happened to require at that commit.
//
// The test asserts the two facts a consumer depends on: the document identity
// (schema_version) and the field set the validator actually enforces.
func TestOverlayPrepareContractFileMatchesValidator(t *testing.T) {
	raw, err := os.ReadFile(contractPath("overlay-prepare.v1.schema.json"))
	if err != nil {
		t.Fatalf("read overlay-prepare contract: %v", err)
	}
	var doc struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Const string `json:"const"`
			Items *struct {
				Required   []string `json:"required"`
				Properties map[string]struct {
					Const string `json:"const"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode overlay-prepare contract: %v", err)
	}

	if got := doc.Properties["schema_version"].Const; got != overlayPrepareSchema {
		t.Fatalf("contract schema_version const = %q, validator accepts %q", got, overlayPrepareSchema)
	}

	// The validator rejects a document missing any of these; the contract must
	// declare them required rather than merely mention them as properties.
	for _, field := range []string{"schema_version", "plan_id", "video_id", "width", "height", "fps_num", "fps_den", "intents"} {
		if !containsString(doc.Required, field) {
			t.Errorf("contract must require %q (validator enforces it)", field)
		}
	}

	items := doc.Properties["intents"].Items
	if items == nil {
		t.Fatal("contract must describe intents items")
	}
	for _, field := range []string{"template_id", "timing_state"} {
		if !containsString(items.Required, field) {
			t.Errorf("intent items must require %q (validator enforces it)", field)
		}
	}
	if got := items.Properties["timing_state"].Const; got != "PENDING" {
		t.Errorf("intent timing_state const = %q, validator requires PENDING", got)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
