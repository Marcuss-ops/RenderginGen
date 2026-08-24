package chronon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

// TestGoldenSemanticOverlayJobV1Immutability locks the semantic golden in
// place: the Go constant must decode, carry the renderinggen.overlay-plan.v1
// contract (with content-addressed asset_refs), stay byte-identical to the
// canonical JSON file, and reference fixtures whose sha256 matches the
// payload. The golden covers the PipelineGen semantic path through the whole
// worker chain: CompileIfSemantic -> materialize -> plan.json ->
// chronon3d_cli -> mp4.
func TestGoldenSemanticOverlayJobV1Immutability(t *testing.T) {
	// 1. The Go constant must decode as a valid renderinggen.job envelope.
	var env struct {
		ID         string          `json:"id"`
		Schema     string          `json:"schema"`
		Version    int             `json:"version"`
		RenderPlan json.RawMessage `json:"render_plan"`
		Assets     []struct {
			Hash        string `json:"hash"`
			LogicalPath string `json:"logical_path"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(GoldenSemanticOverlayJobV1), &env); err != nil {
		t.Fatalf("GoldenSemanticOverlayJobV1 does not decode: %v", err)
	}
	if env.ID != "golden-semantic-overlay-v1" || env.Schema != "renderinggen.job" || env.Version != 1 {
		t.Fatalf("unexpected golden envelope: %+v", env)
	}

	// 2. The render plan must be the SEMANTIC contract, not the concrete one:
	//    PipelineGen owns the decisions, the worker lowers them.
	var plan struct {
		SchemaVersion string `json:"schema_version"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		FPSNum        int    `json:"fps_num"`
		FPSDen        int    `json:"fps_den"`
		Items         []struct {
			ID       string `json:"id"`
			Template string `json:"template_id"`
			Text     string `json:"text"`
			StartMS  int64  `json:"start_ms"`
			EndMS    int64  `json:"end_ms"`
			Assets   []struct {
				ID     string `json:"asset_id"`
				SHA256 string `json:"sha256"`
				URL    string `json:"url"`
			} `json:"asset_refs"`
		} `json:"items"`
	}
	if err := json.Unmarshal(env.RenderPlan, &plan); err != nil {
		t.Fatalf("semantic plan does not decode: %v", err)
	}
	if plan.SchemaVersion != "renderinggen.overlay-plan.v1" {
		t.Fatalf("schema_version = %q, want renderinggen.overlay-plan.v1", plan.SchemaVersion)
	}
	if plan.Width != 1280 || plan.Height != 720 || plan.FPSNum != 30 || plan.FPSDen != 1 {
		t.Fatalf("unexpected canvas (want 1280x720@30): %+v", plan)
	}
	wantItems := map[string]string{
		"background":       "IMAGE_OVERLAY",
		"important_phrase": "IMPORTANT_PHRASE",
		"important_word":   "IMPORTANT_WORD",
		"image_overlay":    "IMAGE_OVERLAY",
	}
	if len(plan.Items) != len(wantItems) {
		t.Fatalf("expected %d items, got %d", len(wantItems), len(plan.Items))
	}
	refs := map[string]string{}
	for _, item := range plan.Items {
		if wantTemplate, ok := wantItems[item.ID]; !ok {
			t.Fatalf("unexpected item id %q", item.ID)
		} else if item.Template != wantTemplate {
			t.Fatalf("item %s: template = %q, want %q", item.ID, item.Template, wantTemplate)
		}
		if item.StartMS < 0 || item.EndMS <= item.StartMS {
			t.Fatalf("item %s: invalid timing %d-%d", item.ID, item.StartMS, item.EndMS)
		}
		for _, ref := range item.Assets {
			if ref.ID == "" || len(ref.SHA256) != 64 || ref.URL == "" {
				t.Fatalf("item %s: incomplete asset ref: %+v", item.ID, ref)
			}
			refs[ref.ID] = ref.SHA256
		}
	}
	// Both IMAGE_OVERLAY items must be content-addressed against the fixtures.
	if refs["background"] != "52209ee36928dba960583179922a54acf045d52d44c3128c517425d4baaa4f78" {
		t.Fatalf("background ref sha256 = %q", refs["background"])
	}
	if refs["apple"] != "ed873745e76173b66999c63546770d9f1426a2189515149176c67637e99a62d6" {
		t.Fatalf("apple ref sha256 = %q", refs["apple"])
	}

	// 3. The Go constant must be byte-identical to the canonical JSON file.
	fileData, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "golden", "golden-semantic-overlay-job-v1.json"))
	if err != nil {
		t.Fatalf("read canonical golden JSON: %v", err)
	}
	var fileJSON map[string]any
	if err := json.Unmarshal(fileData, &fileJSON); err != nil {
		t.Fatalf("canonical golden JSON does not decode: %v", err)
	}
	var goJSON map[string]any
	if err := json.Unmarshal([]byte(GoldenSemanticOverlayJobV1), &goJSON); err != nil {
		t.Fatalf("Go constant does not decode: %v", err)
	}
	if !jsonEqual(t, goJSON, fileJSON) {
		t.Fatalf("GoldenSemanticOverlayJobV1 constant diverges from testdata/golden/golden-semantic-overlay-job-v1.json: update BOTH copies together")
	}

	// 4. Each fixture on disk must hash to the payload hash: the asset_refs
	//    and the job assets carry the same content addresses.
	for _, a := range env.Assets {
		fixture := filepath.Join("..", "..", "..", "testdata", "golden", filepath.Base(a.LogicalPath))
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("read fixture %s: %v", fixture, err)
		}
		if got := storage.Hash(data); got != a.Hash {
			t.Fatalf("fixture %s sha256=%s but payload hash=%s: assets are not deterministic or were regenerated (re-run infra/e2e/gen-golden-assets.py and update both copies)", fixture, got, a.Hash)
		}
	}
}
