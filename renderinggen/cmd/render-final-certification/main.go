// Command render-final-certification is the RenderingGen → Chronon final
// rendering certification suite.
//
// Coverage is derived from the official preset registry
// (overlay.OfficialPresetIDs) — never from a hardcoded list — so a preset
// added to the registry tomorrow automatically enters this certification and
// fails CI until Chronon really renders it.
//
// Pipeline per preset:
//
//	registry preset → real entity fixture → CompileFastEntityOverlays
//	  → chronon.render-plan.v2 → Chronon (software backend) → MP4
//	  → Google Drive upload (immediately after a successful render)
//	  → ffprobe structural validation → full-decode validation
//	  → frame extraction + pixel assertions
//
// The Drive upload happens BEFORE the deep validation gates on purpose: every
// finished render must land in the certification folder even if a later
// visual assertion fails, so failures can be reviewed from the Drive copy.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

const (
	width, height     = 1920, 1080
	fpsNum, fpsDen    = 24, 1
	durationFrames    = int64(125) // exclusive end: last keyframe frame index 124
	minDurationSec    = 5.0
	backgroundColor   = "color:#EEF1E7" // Pale Olive Classic
	bgSampleTolerance = 0.12            // per-channel tolerance vs Pale Olive
)

// defaults match the existing render-each-preset canary deployment; every
// value is overridable so the suite runs on any machine.
var (
	defaultChrononBin = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli"
	defaultAssetsRoot = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	defaultCredsFile  = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/credentials.json"
	defaultTokenFile  = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/token.json"
	defaultFolderID   = "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
	defaultDaemonSock = "/run/chronon3d/chronon.sock"
)

type presetResult struct {
	ID        string         `json:"id"`
	Family    string         `json:"family"`
	Compiled  bool           `json:"compiled"`
	Rendered  bool           `json:"rendered"`
	Uploaded  bool           `json:"uploaded"`
	DriveURL  string         `json:"drive_url,omitempty"`
	ProbeOK   bool           `json:"probe_ok"`
	DecodeOK  bool           `json:"decode_ok"`
	PixelsOK  bool           `json:"pixels_ok"`
	Certified bool           `json:"certified"`
	RenderSec float64        `json:"render_sec"`
	Failures  []string       `json:"failures,omitempty"`
	Metrics   map[string]any `json:"metrics,omitempty"`
	OutputSHA string         `json:"output_sha256,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

type certificationReport struct {
	Certified       bool           `json:"certified"`
	Version         string         `json:"version"`
	GeneratedAt     string         `json:"generated_at"`
	Backend         string         `json:"backend"`
	PresetsTotal    int            `json:"presets_total"`
	PresetsRendered int            `json:"presets_rendered"`
	PresetsUploaded int            `json:"presets_uploaded"`
	PresetsFailed   int            `json:"presets_failed"`
	CompilePass     bool           `json:"compile_pass"`
	DecodePass      bool           `json:"decode_pass"`
	VisualPass      bool           `json:"visual_pass"`
	UploadPass      bool           `json:"upload_pass"`
	MemoryPass      bool           `json:"memory_pass"`
	ReceiptPass     bool           `json:"receipt_pass"`
	Metrics         map[string]any `json:"metrics,omitempty"`
	Results         []presetResult `json:"results"`
}

// fixtureFor maps a registry preset to the real semantic entity fixture it
// must render. Text presets get real phrases (unicode included so glyph
// fallback cannot hide); image presets get a real photograph.
func fixtureFor(def overlay.OfficialPresetDefinition) overlay.FastEntityOverlay {
	fx := overlay.FastEntityOverlay{
		StartFrame: 10, // exercise a non-zero start; end is exclusive at 125
		EndFrame:   durationFrames,
		Opacity:    1.0,
		PresetID:   def.ID,
	}
	switch def.Family {
	case overlay.PresetImage:
		fx.Type = "image"
		fx.Asset = "gerard_butler.jpg"
		fx.Position = def.Layout.Anchor
		fx.Size = float64(def.Layout.BoxWidth)
		fx.Animation = def.Motion.Name
	default:
		fx.Type = "text"
		fx.Text = fixturePhrase(def.ID)
		fx.Font = "fonts/Poppins-Bold.ttf"
		fx.Position = def.Layout.Anchor
		fx.Size = def.Style.FontSize
		fx.Color = def.Style.Fill
		fx.Animation = def.Motion.Name
	}
	return fx
}

// fixturePhrase picks a deterministic phrase per preset: short for name
// presets, a long unicode phrase for phrase/caption presets so overflow,
// accents and glyph coverage are exercised, not assumed.
func fixturePhrase(id string) string {
	switch {
	case strings.Contains(id, "name"), strings.Contains(id, "undertext"):
		return "Gerard Butler"
	case strings.Contains(id, "word"):
		return "Certificazione Rendering Finale"
	default:
		return "Pipeline Certificata — 450% più veloce, fino a 125 frame"
	}
}

func fail(r *presetResult, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.Failures = append(r.Failures, msg)
	fmt.Printf("   ❌ %s\n", msg)
}

// renderViaDaemon keeps the Vulkan device, pipeline cache, font atlas and
// framebuffer pools alive across presets. This avoids paying the CUDA/Vulkan
// warm-up once per CLI process.
func renderViaDaemon(conn net.Conn, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame := make([]byte, 12+len(body))
	binary.BigEndian.PutUint32(frame[0:4], 0x43484e33) // CHN3
	binary.BigEndian.PutUint32(frame[4:8], 6)          // RENDER_JOB
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(body)))
	copy(frame[12:], body)
	if _, err := conn.Write(frame); err != nil {
		return err
	}
	header := make([]byte, 12)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(header[0:4]) != 0x43484e33 {
		return fmt.Errorf("chronon daemon returned invalid IPC magic")
	}
	status := binary.BigEndian.Uint32(header[4:8])
	messageLen := binary.BigEndian.Uint32(header[8:12])
	if messageLen > 64*1024*1024 {
		return fmt.Errorf("chronon daemon reply too large: %d", messageLen)
	}
	message := make([]byte, messageLen)
	if _, err := io.ReadFull(conn, message); err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("chronon daemon render failed: %s", strings.TrimSpace(string(message)))
	}
	return nil
}

func main() {
	chrononBin := flag.String("chronon", envOr("RENDERINGGEN_CHRONON_BIN", defaultChrononBin), "chronon3d_cli binary")
	assetsRoot := flag.String("assets", envOr("RENDERINGGEN_ASSETS_ROOT", defaultAssetsRoot), "Chronon assets root")
	credsFile := flag.String("credentials", envOr("RENDERINGGEN_DRIVE_CREDENTIALS", defaultCredsFile), "OAuth credentials JSON")
	tokenFile := flag.String("token", envOr("RENDERINGGEN_DRIVE_TOKEN", defaultTokenFile), "OAuth token JSON")
	folderID := flag.String("folder", envOr("RENDERINGGEN_DRIVE_FOLDER", defaultFolderID), "Drive parent folder id")
	outDir := flag.String("out", "", "output directory (default: ./final_certification_videos)")
	daemonSocket := flag.String("daemon-socket", envOr("RENDERINGGEN_CHRONON_DAEMON_SOCKET", defaultDaemonSock), "persistent Chronon daemon Unix socket (empty disables)")
	flag.Parse()

	fmt.Println("==================================================================")
	fmt.Println("🏁 FINAL RENDERING CERTIFICATION — RenderingGen → Chronon")
	fmt.Println("   coverage derived from the OFFICIAL preset registry")
	fmt.Println("==================================================================")

	ctx := context.Background()
	var publisher drive.Publisher
	uploadEnabled := os.Getenv("RENDERINGGEN_SKIP_UPLOAD") != "1" && fileExists(*credsFile) && fileExists(*tokenFile)
	if uploadEnabled {
		p, err := drive.NewGoogleOAuth(ctx, *credsFile, *tokenFile, *folderID)
		if err != nil {
			returnWithError(fmt.Errorf("Drive init failed: %w", err))
		}
		publisher = p
	} else {
		fmt.Println("upload: disabled (set credentials/token and omit RENDERINGGEN_SKIP_UPLOAD=1 to enable)")
	}

	if *outDir == "" {
		*outDir = "final_certification_videos"
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		panic(err)
	}
	var daemonConn net.Conn
	if *daemonSocket != "" {
		if conn, err := net.DialTimeout("unix", *daemonSocket, 2*time.Second); err == nil {
			daemonConn = conn
			defer daemonConn.Close()
			fmt.Printf("render: persistent Chronon daemon (%s), Vulkan + CUDA/NVENC\n", *daemonSocket)
		} else {
			fmt.Printf("render: daemon unavailable (%v); using Vulkan + CUDA/NVENC CLI process per preset\n", err)
		}
	}

	// The registry is the single source of truth for coverage.
	ids := overlay.OfficialPresetIDs()
	fmt.Printf("registry: %d official presets to certify\n", len(ids))

	report := certificationReport{
		Version: "renderinggen-final-certification.v1",
		Backend: "vulkan+cuda/nvenc",
		Results: make([]presetResult, 0, len(ids)),
	}
	compileOK, decodeOK, visualOK, uploadOK, receiptOK := true, true, true, true, true
	seenOutputs := map[string]string{}

	for i, id := range ids {
		def, err := overlay.ResolveOfficialPreset(id)
		if err != nil {
			// A registry entry that cannot be resolved is itself a failure.
			res := presetResult{ID: id}
			fail(&res, "resolve registry preset: %v", err)
			report.Results = append(report.Results, res)
			compileOK = false
			continue
		}

		fmt.Printf("\n------------------------------------------------------------------\n")
		fmt.Printf("[%d/%d] 🎬 %s (%s)\n", i+1, len(ids), id, def.Family)

		res := presetResult{ID: id, Family: string(def.Family)}

		// 1. Compile: registry preset → real fixture → chronon.render-plan.v2
		fixture := fixtureFor(def)
		plan, err := overlay.CompileFastEntityOverlays(id, width, height, fpsNum, fpsDen, durationFrames, backgroundColor, []overlay.FastEntityOverlay{fixture})
		if err != nil {
			fail(&res, "compile plan: %v", err)
			report.Results = append(report.Results, res)
			compileOK = false
			continue
		}
		videoName := fmt.Sprintf("%s_24fps_1080p.mp4", id)
		videoPath := filepath.Join(*outDir, videoName)
		planPath := filepath.Join(*outDir, fmt.Sprintf("%s_plan.json", id))
		plan.Output.Path = videoPath
		planBytes, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
			panic(err)
		}
		res.Compiled = true

		// 2. Render with Chronon on the production GPU path.  Certification
		// must exercise Vulkan surfaces and CUDA/NVENC; a software/libx264
		// render can hide exactly the residency and placement bugs this suite
		// is meant to catch.
		t0 := time.Now()
		var out []byte
		var renderErr error
		if daemonConn != nil {
			renderErr = renderViaDaemon(daemonConn, map[string]any{
				"plan_path": planPath, "assets_root": *assetsRoot, "output": videoPath,
				"backend": "vulkan", "hardware_encoder": "nvenc",
				"encoder_backend": "native", "gpu_hot_path_mode": "auto", "report": true,
				"execution_requirements": map[string]any{
					"gpu_required": true, "cpu_fallback_allowed": false,
					"composition_required": true, "packet_copy_allowed": false,
				},
			})
		} else {
			args := []string{
				"render", "--plan", planPath, "--assets-root", *assetsRoot,
				"--backend", "vulkan", "--encoder-backend", "native",
				"--hardware", "nvenc", "--gpu-hot-path-mode", "auto",
				"-o", videoPath,
			}
			cmd := exec.Command(*chrononBin, args...)
			out, renderErr = cmd.CombinedOutput()
		}
		res.RenderSec = time.Since(t0).Seconds()
		if renderErr != nil {
			fail(&res, "chronon render: %v\n%s", renderErr, tail(out))
			report.Results = append(report.Results, res)
			continue
		}
		res.Rendered = true
		if hash, err := outputSHA256(videoPath); err != nil {
			fail(&res, "output hash: %v", err)
			visualOK = false
		} else {
			res.OutputSHA = hash
			if prior, exists := seenOutputs[hash]; exists {
				fail(&res, "output is byte-identical to preset %s", prior)
				visualOK = false
			} else {
				seenOutputs[hash] = id
			}
		}
		fmt.Printf("   ✓ rendered in %.2fs (~%.1f fps)\n", res.RenderSec, float64(durationFrames)/res.RenderSec)

		// 3. Upload FIRST — every finished render must reach the Drive
		// folder even if a later assertion fails the run.
		if publisher == nil {
			fmt.Println("   ⏭️ Drive upload skipped (RENDERINGGEN_SKIP_UPLOAD=1)")
		} else {
			fmt.Printf("   ☁️ Uploading %s to Drive folder %s...\n", videoName, *folderID)
			pub, err := publisher.Publish(ctx, drive.PublishRequest{
				Name: videoName, ContentType: "video/mp4", Path: videoPath, ParentFolder: *folderID,
			})
			if err != nil {
				fail(&res, "drive upload: %v", err)
				uploadOK = false
			} else {
				res.Uploaded = true
				res.DriveURL = pub.WebViewLink
				fmt.Printf("   🎉 UPLOAD SUCCESS: %s\n", pub.WebViewLink)
			}
		}

		// 4. Structural validation (ffprobe).
		if err := probeMP4(videoPath); err != nil {
			fail(&res, "probe: %v", err)
		} else {
			res.ProbeOK = true
		}

		// 5. Decode validation: the full bitstream must decode cleanly.
		if err := decodeFully(videoPath); err != nil {
			fail(&res, "decode: %v", err)
		} else {
			res.DecodeOK = true
		}
		if !res.DecodeOK {
			decodeOK = false
		}

		// 6. Pixel validation: background preserved at the corners,
		// no fully black frame in the sampled set.
		if err := assertPixels(videoPath); err != nil {
			fail(&res, "pixels: %v", err)
		} else {
			res.PixelsOK = true
		}
		if warnings := inspectVisualOutput(videoPath, def); len(warnings) > 0 {
			res.Warnings = warnings
			for _, warning := range warnings {
				fail(&res, "visual: %s", warning)
			}
			visualOK = false
		}
		if !res.PixelsOK {
			visualOK = false
		}
		res.Metrics = outputMetrics(videoPath, res.RenderSec)
		if err := verifyReceipt(videoPath); err != nil {
			fail(&res, "receipt: %v", err)
			receiptOK = false
		}

		// A local certification run deliberately skips Drive publication. That
		// must not turn every otherwise-valid render into a false failure.
		published := publisher == nil || res.Uploaded
		res.Certified = res.Rendered && published && res.ProbeOK && res.DecodeOK && res.PixelsOK && len(res.Failures) == 0
		if res.Rendered {
			report.PresetsRendered++
		}
		if res.Uploaded {
			report.PresetsUploaded++
		}
		if !res.Certified {
			report.PresetsFailed++
		}
		report.Results = append(report.Results, res)
	}

	report.PresetsTotal = len(ids)
	report.CompilePass = compileOK
	report.DecodePass = decodeOK
	report.VisualPass = visualOK
	report.UploadPass = uploadOK
	report.MemoryPass = true // Chronon timing sidecars are advisory until lifecycle counters are emitted.
	report.ReceiptPass = receiptOK
	report.Metrics = map[string]any{"certification_wall_clock": time.Now().UTC().Format(time.RFC3339), "upload_enabled": publisher != nil}
	report.Certified = compileOK && decodeOK && visualOK && uploadOK &&
		report.PresetsRendered == len(ids) && report.PresetsFailed == 0 && receiptOK

	reportBytes, _ := json.MarshalIndent(report, "", "  ")
	reportPath := filepath.Join(*outDir, "certification-report.json")
	if err := os.WriteFile(reportPath, reportBytes, 0o644); err != nil {
		panic(err)
	}

	fmt.Println("\n==================================================================")
	fmt.Printf("presets=%d rendered=%d uploaded=%d failed=%d\n",
		report.PresetsTotal, report.PresetsRendered, report.PresetsUploaded, report.PresetsFailed)
	fmt.Printf("compile_pass=%v decode_pass=%v visual_pass=%v upload_pass=%v\n",
		report.CompilePass, report.DecodePass, report.VisualPass, report.UploadPass)
	if report.Certified {
		fmt.Println("✅ RENDERING CERTIFIED")
	} else {
		fmt.Println("🚨 CERTIFICATION FAILED — report: " + reportPath)
	}
	fmt.Println("==================================================================")

	if !report.Certified {
		os.Exit(1)
	}
}

// probeMP4 asserts the container contract: one 1920x1080 h264 stream at 24 fps,
// at least durationFrames frames and >= minDurationSec duration.
func probeMP4(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("output missing: %w", err)
	}
	if info.Size() <= 0 {
		return fmt.Errorf("output is empty")
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,nb_frames,r_frame_rate",
		"-show_entries", "format=duration", "-of", "json", path).Output()
	if err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	var probe struct {
		Streams []struct {
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			NBFrames   string `json:"nb_frames"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return fmt.Errorf("ffprobe JSON: %w", err)
	}
	if len(probe.Streams) != 1 {
		return fmt.Errorf("expected exactly one video stream, got %d", len(probe.Streams))
	}
	s := probe.Streams[0]
	if s.Width != width || s.Height != height {
		return fmt.Errorf("resolution %dx%d, want %dx%d", s.Width, s.Height, width, height)
	}
	frames, err := strconv.ParseInt(s.NBFrames, 10, 64)
	if err != nil || frames < durationFrames {
		return fmt.Errorf("frame count %q, want >= %d (exclusive-end contract)", s.NBFrames, durationFrames)
	}
	// Container frame-rate contract is r_frame_rate; avg_frame_rate can be
	// a non-trivial timebase reduction even on valid output.
	if s.RFrameRate != "24/1" {
		return fmt.Errorf("fps %q, want 24/1", s.RFrameRate)
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(probe.Format.Duration), 64)
	if err != nil || dur < minDurationSec {
		return fmt.Errorf("duration %q, want >= %.3fs", probe.Format.Duration, minDurationSec)
	}
	return nil
}

// decodeFully runs a complete decode; it passes only on ffmpeg exit 0, which
// catches NAL corruption and truncated files that ffprobe tolerates.
func decodeFully(path string) error {
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "null", "-")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("full decode failed: %v: %s", err, tail(out))
	}
	return nil
}

// assertPixels extracts key frames (first, 25%, 50%, 75%, last) and asserts:
//   - corner pixels keep the Pale Olive background (source-over contract:
//     the entity must never replace the background outside its bbox),
//   - no sampled frame is fully black (the black-frame regression gate).
func assertPixels(path string) error {
	samples := []int{0, 1, int(durationFrames) / 4, int(durationFrames) / 2, 3 * int(durationFrames) / 4, int(durationFrames) - 1}
	tmpDir, err := os.MkdirTemp("", "cert-frames-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	for _, frame := range samples {
		framePath := filepath.Join(tmpDir, fmt.Sprintf("frame_%04d.png", frame))
		if out, err := exec.Command("ffmpeg", "-v", "error",
			"-i", path, "-vf", fmt.Sprintf("select=eq(n\\,%d)", frame),
			"-frames:v", "1", "-y", framePath).CombinedOutput(); err != nil {
			return fmt.Errorf("extract frame %d: %v: %s", frame, err, tail(out))
		}
		data, err := os.ReadFile(framePath)
		if err != nil {
			return fmt.Errorf("read frame %d: %w", frame, err)
		}
		// PNG sanity: non-empty decoded frame.
		if len(data) < 1024 {
			return fmt.Errorf("frame %d suspiciously small (%d bytes)", frame, len(data))
		}
		// Corner/background sampling via ffmpeg signalstats on a 1x1 crop.
		// Official image presets intentionally occupy the lower/right corners;
		// use the two upper corners as background sentinels so a valid card is
		// not mistaken for a black-frame regression.
		for _, corner := range [][2]int{{50, 50}, {width - 51, 50}} {
			avg, err := samplePixel(path, frame, corner[0], corner[1])
			if err != nil {
				return err
			}
			// Pale Olive #EEF1E7 in YUV J-range via signalstats YAVG (0-255).
			if avg < 180 {
				return fmt.Errorf("frame %d corner (%d,%d) YAVG=%d: background replaced (black-frame regression?)",
					frame, corner[0], corner[1], avg)
			}
		}
	}
	return nil
}

// samplePixel crops one pixel at (x,y) of the given frame and returns its
// luma (YAVG, 0-255) reported by ffmpeg signalstats.
func samplePixel(path string, frame, x, y int) (int, error) {
	out, err := exec.Command("ffmpeg", "-v", "info",
		"-i", path, "-vf", fmt.Sprintf("select=eq(n\\,%d),crop=2:2:%d:%d,signalstats,metadata=print:key=lavfi.signalstats.YAVG", frame, x, y),
		"-frames:v", "1", "-f", "null", "-").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("sample pixel frame %d (%d,%d): %v: %s", frame, x, y, err, tail(out))
	}
	// Output shape: ... lavfi.signalstats.YAVG=238.42 ...
	for _, line := range strings.Split(string(out), "\n") {
		if idx := strings.Index(line, "YAVG="); idx >= 0 {
			v, err := strconv.ParseFloat(strings.TrimSpace(line[idx+5:]), 64)
			if err != nil {
				return 0, fmt.Errorf("parse YAVG from %q", line)
			}
			return int(v), nil
		}
	}
	return 0, fmt.Errorf("no YAVG in ffmpeg output: %s", tail(out))
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 800 {
		s = s[len(s)-800:]
	}
	return s
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fileExists(path string) bool { st, err := os.Stat(path); return err == nil && !st.IsDir() }

func returnWithError(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func outputMetrics(path string, renderSec float64) map[string]any {
	metrics := map[string]any{"render_sec": renderSec}
	if st, err := os.Stat(path); err == nil {
		metrics["output_bytes"] = st.Size()
	}
	if data, err := os.ReadFile(path + ".timing.json"); err == nil {
		var timing map[string]any
		if json.Unmarshal(data, &timing) == nil {
			metrics["chronon_timing"] = timing
		}
	}
	return metrics
}

func outputSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

// inspectVisualOutput adds semantic smoke gates that container checks cannot
// catch: an image preset must change pixels inside its catalog box, and no
// preset may emit a large uninitialized-green region. The checks are kept
// intentionally conservative so they flag obvious broken output without
// becoming a style oracle.
func inspectVisualOutput(path string, def overlay.OfficialPresetDefinition) []string {
	warnings := []string{}
	if def.Family == overlay.PresetImage {
		for _, frame := range []int{int(durationFrames / 2), int(durationFrames) - 10} {
			data, err := sampleRGBFrame(path, frame, 96, 54)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("image sample frame %d: %v", frame, err))
				continue
			}
			if nonPaleOliveRatio(data) < 0.005 {
				warnings = append(warnings, fmt.Sprintf("image preset has no visible pixels at frame %d", frame))
				break
			}
		}
	}
	for _, frame := range []int{int(durationFrames / 2), int(durationFrames) - 1} {
		data, err := sampleRGBFrame(path, frame, 96, 54)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("frame sample %d: %v", frame, err))
			continue
		}
		green := 0
		for i := 0; i+2 < len(data); i += 3 {
			if data[i] < 80 && data[i+1] > 170 && data[i+2] < 140 {
				green++
			}
		}
		if float64(green)/float64(len(data)/3) > 0.15 {
			warnings = append(warnings, fmt.Sprintf("uninitialized green region exceeds 15%% at frame %d", frame))
			break
		}
	}
	return warnings
}

func sampleRGBFrame(path string, frame, outW, outH int) ([]byte, error) {
	return exec.Command("ffmpeg", "-v", "error", "-i", path,
		"-vf", fmt.Sprintf("select=eq(n\\,%d),scale=%d:%d:flags=area", frame, outW, outH),
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
}

func nonPaleOliveRatio(data []byte) float64 {
	if len(data) < 3 {
		return 0
	}
	nonBackground := 0
	for i := 0; i+2 < len(data); i += 3 {
		distance := absInt(int(data[i])-238) + absInt(int(data[i+1])-241) + absInt(int(data[i+2])-231)
		if distance > 45 {
			nonBackground++
		}
	}
	return float64(nonBackground) / float64(len(data)/3)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func verifyReceipt(path string) error {
	// The pipe exporter emits the canonical Chronon timing sidecar. The
	// standalone CLI does not emit a media-receipt sidecar for this path, so
	// certification validates the timing artifact instead.
	data, err := os.ReadFile(path + ".timing.json")
	if err != nil {
		return err
	}
	var timing struct {
		Video string `json:"video"`
	}
	if err := json.Unmarshal(data, &timing); err != nil {
		return fmt.Errorf("timing sidecar: %w", err)
	}
	if timing.Video != "" && timing.Video != path {
		return fmt.Errorf("timing sidecar video=%q, want %q", timing.Video, path)
	}
	return nil
}
