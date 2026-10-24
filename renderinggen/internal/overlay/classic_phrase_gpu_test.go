package overlay

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This opt-in canary proves the newly catalogued regular Playfair face and
// classic phrase motion execute on the strict Vulkan/NVENC lane. Set
// CHRONON_BIN to enable it; the normal unit suite never invokes a GPU.
func TestClassicPhrasePresetExecutesOnStrictGPU(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"classic-phrase-gpu","video_id":"classic-phrase-gpu","width":1280,"height":720,"fps_num":24,"fps_den":1,"background":{"kind":"color","color":[1,1,1,1]},"items":[{"id":"classic","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"classic_phrase","text":"A CLASSIC PHRASE","start_ms":0,"end_ms":2000}]}`)
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "classic-phrase.mp4")
	compiled.Plan.Output.Path = output
	planBytes, err := json.MarshalIndent(compiled.Plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "vulkan", "--hardware", "nvenc", "--encoder-backend", "native",
		"--gpu-hot-path-mode", "require_gpu_native", "--encode-preset", "p1", "--rate-control", "qp", "--qp", "23", "-o", output)
	cmd.Dir = assetsRoot
	if rendered, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("strict Vulkan/NVENC render failed: %v\n%s", err, rendered)
	}
	if err := verifyCatalogMotionOutput(output, 1280, 720, true); err != nil {
		t.Fatalf("verify strict GPU artifact: %v", err)
	}
}
