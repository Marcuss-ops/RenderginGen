package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompiledMultiImagePlansPassChrononValidation hands the concrete plans this
// package emits to the real Chronon render-plan validator. The unit suite
// proves the lowering is self-consistent; this test proves the wire document is
// executable, which is the contract the processor depends on.
//
// Opt-in like every real-engine test: CHRONON_BIN must name chronon3d_cli.
// `validate` exits 0 even when the plan is rejected, so the assertion is the
// JSON `valid` field, not the process status.
func TestCompiledMultiImagePlansPassChrononValidation(t *testing.T) {
	bin := os.Getenv("CHRONON_BIN")
	if bin == "" {
		t.Skip("set CHRONON_BIN to a chronon3d_cli binary to run the render-plan validator")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("CHRONON_BIN %q: %v", bin, err)
	}

	cases := []struct {
		name  string
		raw   []byte
		sizes map[string]ImageSize
	}{
		{
			name: "camera-pan-chain",
			raw:  panChainPlanPayload("chronon-validate-chain-pan"),
			sizes: map[string]ImageSize{
				"assets/semantic/asset-ada.jpg":   {Width: 1600, Height: 900},
				"assets/semantic/asset-grace.jpg": {Width: 1600, Height: 900},
				"assets/semantic/asset-alan.jpg":  {Width: 1600, Height: 900},
			},
		},
		{
			name: "entity-card",
			raw:  sizedEntityPlan(),
			sizes: map[string]ImageSize{
				"assets/semantic/portrait.png": {Width: 1600, Height: 900},
			},
		},
		{
			name: "composite-group",
			raw:  compositeGroupPlanPayload("chronon-validate-composite"),
			sizes: map[string]ImageSize{
				"assets/semantic/wide.png": {Width: 1600, Height: 900},
				"assets/semantic/tall.png": {Width: 900, Height: 1600},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := CompileSemanticWithAssetSizes(tc.raw, tc.sizes)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			root := t.TempDir()
			writeValidationAssets(t, root, compiled.Plan.Layers)
			planBytes, err := compiled.Plan.MarshalIndent("", "  ")
			if err != nil {
				t.Fatalf("marshal plan: %v", err)
			}
			planPath := filepath.Join(root, "plan.json")
			if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
				t.Fatalf("write plan: %v", err)
			}

			cmd := exec.Command(bin, "validate", "--plan", planPath, "--assets-root", root)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, runErr := cmd.Output()
			var report struct {
				Valid   bool   `json:"valid"`
				Status  string `json:"status"`
				Error   string `json:"error"`
				Message string `json:"message"`
				Path    string `json:"path"`
			}
			decodeErr := json.Unmarshal(chrononJSONReport(out), &report)
			if runErr != nil {
				t.Fatalf("chronon validate failed: %v\nstderr:\n%s\nstdout:\n%s", runErr, stderr.String(), out)
			}
			if decodeErr != nil {
				t.Fatalf("chronon validate emitted no JSON report: %v\nstdout:\n%s\nstderr:\n%s", decodeErr, out, stderr.String())
			}
			if !report.Valid {
				t.Fatalf("chronon rejected %s: status=%q error=%q message=%q path=%q", tc.name, report.Status, report.Error, report.Message, report.Path)
			}
			t.Logf("chronon accepted %s: schema=%s version=%d layers=%d",
				tc.name, compiled.Plan.Schema, compiled.Plan.Version, len(compiled.Plan.Layers))
		})
	}
}

// chrononJSONReport returns the validator's JSON report. The CLI writes
// asset-profile and plan-profile log lines to stdout before the report, so the
// document starts at the first line whose first byte is '{'.
func chrononJSONReport(stdout []byte) []byte {
	lines := bytes.Split(stdout, []byte("\n"))
	for index, line := range lines {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("{")) {
			return bytes.Join(lines[index:], []byte("\n"))
		}
	}
	return stdout
}

// compositeGroupPlanPayload is the composite group fixture used by the parity
// and validator tests: two members with opposite source aspects and a caption
// each.
func compositeGroupPlanPayload(planID string) []byte {
	return []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":%q,"video_id":%q,
		"width":1920,"height":1080,"fps_num":30,"fps_den":1,"duration_ms":6000,
		"items":[{"id":"cmp","kind":"image","template_id":"IMAGE_OVERLAY","preset_id":"image_scale_in","group_motion_id":"image_group_slide_right",
			"start_ms":0,"end_ms":6000,
			"asset_refs":[
				{"asset_id":"wide","sha256":%q,"url":"https://fixtures.example/wide.png","media_type":"image/png"},
				{"asset_id":"tall","sha256":%q,"url":"https://fixtures.example/tall.png","media_type":"image/png"}],
			"image_layers":[
				{"id":"wide","asset_id":"wide","start_ms":0,"end_ms":3000,"caption":"Wide","preset_id":"image_scale_in","params":{"width":480,"height":480}},
				{"id":"tall","asset_id":"tall","start_ms":3000,"end_ms":6000,"caption":"Tall","preset_id":"image_scale_in","params":{"width":480,"height":480}}]}]}`,
		planID, planID, strings.Repeat("a", 64), strings.Repeat("b", 64)))
}

// panChainPlanPayload is the shared camera-pan fixture used by the parity and
// validator tests: three landscape members with a caption each.
func panChainPlanPayload(planID string) []byte {
	return []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":%q,"video_id":%q,
		"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":9000,
		"items":[{
			"id":"chain","kind":"image_chain","template_id":"IMAGE_CHAIN",
			"start_ms":0,"end_ms":9000,
			"asset_refs":[%s,%s,%s],
			"params":{
				"chain_camera":"image_group_camera_pan_right","chain_count":3,
				"chain_segments":[%s,%s,%s]
			}
		}]
	}`, planID, planID,
		panTestAsset("ada"), panTestAsset("grace"), panTestAsset("alan"),
		panTestSegment("ada", "Ada", 0),
		panTestSegment("grace", "Grace", 3_000_000),
		panTestSegment("alan", "Alan", 6_000_000),
	))
}

// writeValidationAssets creates the files the validator's asset preflight
// resolves: the module's real fonts plus a valid image for every image layer,
// at the box size the plan declares.
func writeValidationAssets(t *testing.T, root string, layers []Layer) {
	t.Helper()
	fontsDir := filepath.Join("..", "..", "assets", "fonts")
	fonts, err := os.ReadDir(fontsDir)
	if err != nil {
		t.Fatalf("read module fonts: %v", err)
	}
	for _, entry := range fonts {
		data, err := os.ReadFile(filepath.Join(fontsDir, entry.Name()))
		if err != nil {
			t.Fatalf("read font %s: %v", entry.Name(), err)
		}
		writeAssetFile(t, root, filepath.Join("assets", "fonts", entry.Name()), data)
	}
	for _, layer := range layers {
		if layer.Type != "image" || layer.Asset == "" || strings.Contains(layer.Asset, "://") {
			continue
		}
		width, height := layer.BoxWidth, layer.BoxHeight
		if width <= 0 || height <= 0 {
			width, height = 640, 360
		}
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		var buf bytes.Buffer
		switch strings.ToLower(filepath.Ext(layer.Asset)) {
		case ".jpg", ".jpeg":
			if err := jpeg.Encode(&buf, img, nil); err != nil {
				t.Fatalf("encode jpeg for %s: %v", layer.Asset, err)
			}
		default:
			if err := png.Encode(&buf, img); err != nil {
				t.Fatalf("encode png for %s: %v", layer.Asset, err)
			}
		}
		writeAssetFile(t, root, layer.Asset, buf.Bytes())
	}
}

func writeAssetFile(t *testing.T, root, logical string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.Clean("/"+logical))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", logical, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", logical, err)
	}
}
