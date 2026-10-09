package overlay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const semanticSFXSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func semanticSFXPlan(soundEffect string) []byte {
	return []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"semantic-sfx-test","video_id":"source-video",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"items":[{
			"id":"photo","kind":"image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":1000,"end_ms":2000,
			"asset_refs":[{"asset_id":"photo","sha256":"` + semanticSFXSHA + `","url":"assets/semantic/photo.png","media_type":"image/png"}],
			"params":{"width":320,"height":240},
			"sound_effect":` + soundEffect + `
		}]
	}`)
}

func TestCompileSemanticImageSFXLowersToChrononV3AudioClip(t *testing.T) {
	sfx := `{"asset_ref":{"asset_id":"cue","sha256":"` + semanticSFXSHA + `","url":"assets/semantic/overlay-sfx-` + semanticSFXSHA + `.m4a","media_type":"audio/mp4"},"duration_ms":250,"gain_db":-18}`
	result, err := CompileSemantic(semanticSFXPlan(sfx))
	if err != nil {
		t.Fatalf("compile image SFX plan: %v", err)
	}
	if result.Plan.Schema != RenderPlanSchemaV3 || result.Plan.Version != RenderPlanVersionV3 {
		t.Fatalf("SFX plan schema = %s v%d, want v3", result.Plan.Schema, result.Plan.Version)
	}
	if result.Plan.Audio == nil || len(result.Plan.Audio.Clips) != 1 {
		t.Fatalf("compiled audio clips = %+v, want one", result.Plan.Audio)
	}
	clip := result.Plan.Audio.Clips[0]
	if clip.ID != "overlay-sfx-photo" || clip.Asset != "assets/semantic/overlay-sfx-"+semanticSFXSHA+".m4a" || clip.Start != 24 || clip.ClipIn != 0 || clip.ClipOut != 6 || clip.GainDB != -18 || clip.Role != "sfx" {
		t.Fatalf("lowered clip = %+v, want 24-frame start, 6-frame source trim, -18dB sfx", clip)
	}
	found := false
	for _, asset := range result.Assets {
		if asset.LogicalPath == clip.Asset && asset.Hash == semanticSFXSHA {
			found = true
		}
	}
	if !found {
		t.Fatalf("SFX cue missing from registered asset manifest: %+v", result.Assets)
	}
	wire, err := json.Marshal(result.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"audio":{"clips":[`) || strings.Contains(string(wire), `"version":2`) {
		t.Fatalf("Chronon v3 audio wire missing: %s", wire)
	}
}

func TestCompileSemanticCompositeSFXUsesFirstVisibleChildAndCapsClip(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"composite-sfx-test","video_id":"source-video",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"items":[{
			"id":"pair","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":0,"end_ms":2000,"duration_ms":2000,
			"asset_refs":[
				{"asset_id":"later","sha256":"` + semanticSFXSHA + `","url":"assets/semantic/later.png","media_type":"image/png"},
				{"asset_id":"earlier","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"assets/semantic/earlier.png","media_type":"image/png"}
			],
			"image_layers":[
				{"id":"later","asset_id":"later","start_ms":500,"end_ms":1200,"preset_id":"image_focus_in","motion_params":{},"params":{"width":100,"height":100}},
				{"id":"earlier","asset_id":"earlier","start_ms":100,"end_ms":220,"preset_id":"image_focus_in","motion_params":{},"params":{"width":100,"height":100}}
			],
			"sound_effect":{"asset_ref":{"asset_id":"cue","sha256":"` + semanticSFXSHA + `","url":"assets/semantic/cue.m4a","media_type":"audio/mp4"},"start_offset_ms":100,"duration_ms":120,"gain_db":-6}
		}]
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite SFX plan: %v", err)
	}
	if result.Plan.Audio == nil || len(result.Plan.Audio.Clips) != 1 {
		t.Fatalf("audio clips = %+v, want exactly one", result.Plan.Audio)
	}
	clip := result.Plan.Audio.Clips[0]
	if clip.Start != 2 || clip.ClipOut != 3 {
		t.Fatalf("composite clip = %+v, want first visible frame 2 and source trim 3 frames", clip)
	}
}

func TestImageSFXRealChrononRenderHasBoundedNonSilentAudio(t *testing.T) {
	bin := chrononBinFor(t)
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skipf("ffprobe unavailable for audio stream verification: %v", err)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable for audio energy verification: %v", err)
	}
	assetsRoot := t.TempDir()
	imagePath := filepath.Join(assetsRoot, "photo.png")
	fixtureImage := image.NewRGBA(image.Rect(0, 0, 160, 90))
	for y := 0; y < fixtureImage.Bounds().Dy(); y++ {
		for x := 0; x < fixtureImage.Bounds().Dx(); x++ {
			fixtureImage.SetRGBA(x, y, color.RGBA{R: 40, G: 120, B: 210, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, fixtureImage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cuePath := filepath.Join(assetsRoot, "cue.m4a")
	gen := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=1000:duration=0.7", "-c:a", "aac", "-b:a", "96k", "-y", cuePath)
	if output, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test SFX: %v\n%s", err, output)
	}
	imageHash := fixtureSHA256(t, imagePath)
	cueHash := fixtureSHA256(t, cuePath)
	logicalImage := "assets/semantic/photo.png"
	logicalCue := "assets/semantic/cue.m4a"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(assetsRoot, logicalImage)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(assetsRoot, logicalCue)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(imagePath, filepath.Join(assetsRoot, logicalImage)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cuePath, filepath.Join(assetsRoot, logicalCue)); err != nil {
		t.Fatal(err)
	}
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-sfx-e2e","video_id":"image-sfx-e2e",
		"width":160,"height":90,"fps_num":24,"fps_den":1,"duration_ms":500,
		"background":{"kind":"color","color":[0.02,0.02,0.02,1]},
		"items":[{"id":"photo","kind":"image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":100,"end_ms":350,
			"asset_refs":[{"asset_id":"photo","sha256":%q,"url":%q,"media_type":"image/png"}],
			"params":{"width":100,"height":70},
			"sound_effect":{"asset_ref":{"asset_id":"cue","sha256":%q,"url":%q,"media_type":"audio/mp4"},"duration_ms":250,"gain_db":-6}
		}]
	}`, imageHash, logicalImage, cueHash, logicalCue))
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile real SFX render plan: %v", err)
	}
	if len(compiled.Plan.Audio.Clips) != 1 || compiled.Plan.Audio.Clips[0].Start != 2 || compiled.Plan.Audio.Clips[0].ClipOut != 6 {
		t.Fatalf("render plan audio clip = %+v; want frame 2 to 8 at 24fps", compiled.Plan.Audio)
	}
	outputPath := filepath.Join(t.TempDir(), "image-sfx-render.mp4")
	compiled.Plan.Output.Path = outputPath
	planPath := filepath.Join(t.TempDir(), "image-sfx-plan.json")
	planBytes, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "native", "--hardware", "none", "--gpu-hot-path-mode", "auto", "--encode-preset", "ultrafast", "-o", outputPath)
	cmd.Dir = assetsRoot
	if result, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Chronon SFX render failed: %v\n%s", err, tailBytes(result))
	}
	probe := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_type", "-show_entries", "format=duration", "-of", "json", outputPath)
	probeBytes, err := probe.Output()
	if err != nil {
		t.Fatalf("ffprobe rendered SFX output: %v", err)
	}
	var facts struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(probeBytes, &facts); err != nil {
		t.Fatal(err)
	}
	hasAudio := false
	for _, stream := range facts.Streams {
		hasAudio = hasAudio || stream.CodecType == "audio"
	}
	if !hasAudio {
		t.Fatalf("rendered file has no audio stream: %s", probeBytes)
	}
	var duration float64
	if _, err := fmt.Sscanf(facts.Format.Duration, "%f", &duration); err != nil || duration < 0.45 || duration > 0.60 {
		t.Fatalf("render duration=%q (%f), want approximately 0.5s", facts.Format.Duration, duration)
	}
	pcmPath := filepath.Join(t.TempDir(), "rendered-sfx.pcm")
	decode := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-i", outputPath, "-map", "0:a:0", "-t", "0.8", "-f", "s16le", "-ac", "1", "-ar", "8000", "-y", pcmPath)
	if output, err := decode.CombinedOutput(); err != nil {
		t.Fatalf("decode rendered audio: %v\n%s", err, output)
	}
	pcm, err := os.ReadFile(pcmPath)
	if err != nil {
		t.Fatal(err)
	}
	var peak int16
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}
	if peak < 150 {
		t.Fatalf("rendered SFX peak sample=%d; expected non-silent cue", peak)
	}
}

func TestCompileSemanticSFXRejectsInvalidEligibilityTimingAndGain(t *testing.T) {
	valid := `{"asset_ref":{"asset_id":"cue","sha256":"` + semanticSFXSHA + `","url":"assets/semantic/cue.m4a","media_type":"audio/mp4"},"duration_ms":250,"gain_db":-18}`
	cases := []struct {
		name string
		plan []byte
	}{
		{"wrong kind", []byte(strings.Replace(string(semanticSFXPlan(valid)), `"kind":"image"`, `"kind":"text_phrase"`, 1))},
		{"video kind", []byte(strings.Replace(string(semanticSFXPlan(valid)), `"kind":"image"`, `"kind":"video"`, 1))},
		{"outside item window", semanticSFXPlan(strings.Replace(valid, `"duration_ms":250`, `"duration_ms":1500`, 1))},
		{"out of range gain", semanticSFXPlan(strings.Replace(valid, `"gain_db":-18`, `"gain_db":25`, 1))},
		{"non audio asset", semanticSFXPlan(strings.Replace(valid, `"media_type":"audio/mp4"`, `"media_type":"image/png"`, 1))},
		{"nonzero single image offset", semanticSFXPlan(strings.Replace(valid, `"duration_ms":250`, `"duration_ms":250,"start_offset_ms":1`, 1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CompileSemantic(tc.plan); err == nil {
				t.Fatal("invalid SFX semantic input unexpectedly compiled")
			}
		})
	}
}
