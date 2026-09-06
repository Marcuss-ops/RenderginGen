package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

type PresetItem struct {
	ID          string
	Kind        string // "image" or "text"
	PresetName  string
	Title       string
	Description string
	Overlay     overlay.FastEntityOverlay
}

func validateMP4(path string, minDuration float64, width, height int, minFrames int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("output missing: %w", err)
	}
	if info.Size() <= 0 {
		return fmt.Errorf("output is empty")
	}
	probe := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,nb_frames",
		"-show_entries", "format=duration", "-of", "json", path)
	out, err := probe.Output()
	if err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	var result struct {
		Streams []struct {
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			NBFrames string `json:"nb_frames"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("ffprobe JSON: %w", err)
	}
	if len(result.Streams) != 1 {
		return fmt.Errorf("expected one video stream, got %d", len(result.Streams))
	}
	stream := result.Streams[0]
	if stream.Width != width || stream.Height != height {
		return fmt.Errorf("resolution %dx%d, expected %dx%d", stream.Width, stream.Height, width, height)
	}
	frames, err := strconv.ParseInt(stream.NBFrames, 10, 64)
	if err != nil || frames < minFrames {
		return fmt.Errorf("frame count %q, expected at least %d", stream.NBFrames, minFrames)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(result.Format.Duration), 64)
	if err != nil || duration < minDuration {
		return fmt.Errorf("duration %q, expected at least %.3fs", result.Format.Duration, minDuration)
	}
	return nil
}

func main() {
	fmt.Println("==================================================================")
	fmt.Println("🎬 RENDERING SEPARATO PER OGNI SINGOLO PRESET (24 FPS)")
	fmt.Println("==================================================================")

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	// Use the real Pale Olive Classic motion background and a real celebrity
	// photograph. The preset canary must exercise the same image class used by
	// the runtime entity path; synthetic PNG fixtures are not representative.
	bgVideo := "color:#EEF1E7" // Pale Olive Classic; concrete Chronon color layer
	// The direct CLI canary mounts testdata/golden as the asset root; the
	// checked-in font therefore lives at its root (the worker's semantic path
	// is assets/fonts/Poppins-Bold.ttf after workspace materialisation).
	fontPath := "Poppins-Bold.ttf"
	assetsRoot := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	chrononBin := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli"

	folderID := "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
	credsFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/credentials.json"
	tokenFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/token.json"

	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, credsFile, tokenFile, folderID)
	if err != nil {
		panic(fmt.Errorf("Drive init failed: %w", err))
	}

	// 5 seconds minimum @ 24 fps; 125 frames keeps encoded duration above 5s.
	durationFrames := int64(125)

	presets := []PresetItem{
		// ── PRESET IMMAGINI ──
		{
			ID:          "preset_image_scale_in",
			Kind:        "image",
			PresetName:  "image_scale_in",
			Title:       "Image Scale In (Pop)",
			Description: "Entrata con scala dinamica morbida da 0.85x a 1.0x",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_image_slide_left",
			Kind:        "image",
			PresetName:  "image_slide_left",
			Title:       "Image Slide Left",
			Description: "Entrata laterale fluida da sinistra verso il centro",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "slide_in",
				Opacity:    1.0,
				Translate:  []float64{0, 0},
			},
		},
		{
			ID:          "preset_image_slide_right",
			Kind:        "image",
			PresetName:  "image_slide_right",
			Title:       "Image Slide Right",
			Description: "Entrata laterale da destra verso il centro",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "slide_in",
				Opacity:    1.0,
				Translate:  []float64{0, 0},
			},
		},
		{
			ID:          "preset_image_focus_in",
			Kind:        "image",
			PresetName:  "image_focus_in",
			Title:       "Image Focus In (Zoom)",
			Description: "Zoom progressivo morbido al centro dell'attenzione",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "focus_in",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_modern_rounded_pop",
			Kind:        "image",
			PresetName:  "modern_rounded_pop",
			Title:       "Modern Rounded Pop (SDF)",
			Description: "Card con angoli arrotondati calcolati in tempo reale via SDF CUDA",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_bottom_card_rise",
			Kind:        "image",
			PresetName:  "bottom_card_rise",
			Title:       "Bottom Card Rise",
			Description: "Risalita dal bordo inferiore dello schermo (ottimale per grafici/card)",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "reveal_from_bottom",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_image_fade_in",
			Kind:        "image",
			PresetName:  "image_fade_in",
			Title:       "Image Fade In",
			Description: "Dissolvenza classica pulita a centro schermo",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   125,
				Position:   "center",
				Size:       600,
				Asset:      "gerard_butler.jpg",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},

		// ── PRESET FRASI / TESTO ──
		{
			ID:          "preset_lower_third_safe",
			Kind:        "text",
			PresetName:  "lower_third_safe",
			Title:       "Lower Third Safe (Nome Entità)",
			Description: "Didascalia nome/ruolo posizionata nella safe-area inferiore",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "Gerard Butler",
				Font:       fontPath,
				Size:       72,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "lower_third",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_caption_card",
			Kind:        "text",
			PresetName:  "caption_card",
			Title:       "Caption Card (Citazione / Didascalia)",
			Description: "Testo informativo a centro schermo con dissolvenza morbida",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "Hollywood Lead Actor & Producer",
				Font:       fontPath,
				Size:       54,
				Color:      []float64{0.95, 0.95, 1.0, 1.0},
				Position:   "center",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_clean_slide_up",
			Kind:        "text",
			PresetName:  "clean_slide_up",
			Title:       "Clean Slide Up (Headline)",
			Description: "Frase a comparsa dal basso fluida per titoli e sezioni",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "Global Technology Infrastructure",
				Font:       fontPath,
				Size:       58,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "center",
				Animation:  "reveal_from_bottom",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_phrase_scale_in",
			Kind:        "text",
			PresetName:  "phrase_scale_in",
			Title:       "Phrase Scale In / Snap Scale",
			Description: "Ingresso a scala pop dinamico per dati numerici e callout",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "+450% GPU Rendering Speed",
				Font:       fontPath,
				Size:       64,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "center",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},
		{
			ID:          "preset_phrase_fade_in",
			Kind:        "text",
			PresetName:  "phrase_fade_in",
			Title:       "Phrase Fade In",
			Description: "Dissolvenza testo pulita per frasi di chiusura o narrazione",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   125,
				Text:       "Fast Entity Overlay Pipeline — Active",
				Font:       fontPath,
				Size:       52,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "center",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},
	}
	if os.Getenv("RENDERINGGEN_IMAGE_PRESETS_ONLY") == "1" {
		imagePresets := presets[:0]
		for _, preset := range presets {
			if preset.Kind == "image" {
				imagePresets = append(imagePresets, preset)
			}
		}
		presets = imagePresets
	}

	outDir := filepath.Join(cwd, "preset_videos")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		panic(err)
	}

	type ResultSummary struct {
		ID         string  `json:"id"`
		PresetName string  `json:"preset_name"`
		Title      string  `json:"title"`
		FileID     string  `json:"file_id"`
		DriveURL   string  `json:"drive_url"`
		RenderSec  float64 `json:"render_sec"`
	}
	var results []ResultSummary

	for idx, item := range presets {
		fmt.Printf("\n------------------------------------------------------------------\n")
		fmt.Printf("[%d/%d] 🚀 Rendering Preset: %s (%s)\n", idx+1, len(presets), item.PresetName, item.Title)
		fmt.Printf("   Descrizione: %s\n", item.Description)

		videoFileName := fmt.Sprintf("%s_24fps_1080p.mp4", item.ID)
		videoPath := filepath.Join(outDir, videoFileName)
		planPath := filepath.Join(outDir, fmt.Sprintf("%s_plan.json", item.ID))
		// Route every canary through the official preset catalog so text layout
		// and image placement are resolved by the same authority as production.
		item.Overlay.PresetID = item.PresetName

		// 1. Build Plan
		plan, err := overlay.CompileFastEntityOverlays(
			item.ID,
			1920, 1080,
			24, 1,
			durationFrames,
			bgVideo,
			[]overlay.FastEntityOverlay{item.Overlay},
		)
		if err != nil {
			fmt.Printf("❌ BuildPlan error: %v\n", err)
			continue
		}
		plan.Output.Path = videoPath

		planBytes, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(planPath, planBytes, 0644); err != nil {
			panic(err)
		}

		// 2. Render with Chronon compositor. Presets are authored
		// compositions (background + image/text), so use the compositor pipe
		// backend; direct-YUV is reserved for source-video-only paths.
		args := []string{
			"render",
			"--plan", planPath,
			"--assets-root", assetsRoot,
			"--backend", "software",
			"--encoder-backend", "pipe",
			"--hardware", "none",
			"--gpu-hot-path-mode", "auto",
			"-o", videoPath,
		}

		t0 := time.Now()
		cmd := exec.Command(chrononBin, args...)
		out, err := cmd.CombinedOutput()
		renderSec := time.Since(t0).Seconds()
		if err != nil {
			fmt.Printf("❌ Chronon render failed for %s: %v\n%s\n", item.ID, err, string(out))
			continue
		}
		if err := validateMP4(videoPath, 5.0, 1920, 1080, durationFrames); err != nil {
			fmt.Printf("❌ Invalid MP4 for %s: %v\n", item.ID, err)
			continue
		}
		fmt.Printf("✓ Render completato in %.2fs (~%.1f FPS)\n", renderSec, float64(durationFrames)/renderSec)

		if os.Getenv("RENDERINGGEN_SKIP_UPLOAD") != "" {
			fmt.Println("⏭️ Upload saltato (RENDERINGGEN_SKIP_UPLOAD=1)")
			results = append(results, ResultSummary{ID: item.ID, PresetName: item.PresetName, Title: item.Title, RenderSec: renderSec})
			continue
		}

		// 3. Upload to Google Drive
		fmt.Printf("☁️ Uploading %s to Drive folder %s...\n", videoFileName, folderID)
		res, err := publisher.Publish(ctx, drive.PublishRequest{
			Name:         videoFileName,
			ContentType:  "video/mp4",
			Path:         videoPath,
			ParentFolder: folderID,
		})
		if err != nil {
			fmt.Printf("❌ Drive upload failed: %v\n", err)
			continue
		}

		fmt.Printf("🎉 UPLOAD SUCCESS: %s\n", res.WebViewLink)
		results = append(results, ResultSummary{
			ID:         item.ID,
			PresetName: item.PresetName,
			Title:      item.Title,
			FileID:     res.FileID,
			DriveURL:   res.WebViewLink,
			RenderSec:  renderSec,
		})
	}

	summaryBytes, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "all_presets_upload_summary.json"), summaryBytes, 0644)

	fmt.Println("\n==================================================================")
	fmt.Printf("🏁 TUTTI I %d PRESET SONO STATI RENDERIZZATI E CARICATI SINGOLARMENTE!\n", len(results))
	fmt.Println("==================================================================")
}
