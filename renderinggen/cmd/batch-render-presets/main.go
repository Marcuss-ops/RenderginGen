// Command batch-render-presets renders a matrix of overlay presets described by
// a JSON manifest.
//
// The matrix used to be twenty Go literals in this file, the semantic plan was
// assembled with fmt.Sprintf over a JSON template, paths were resolved by walking
// parent directories until a folder named "RenderingGen" turned up, and the
// render/verify/upload steps were hand-rolled around `chronon3d_cli` and a
// `drive-upload` subprocess. This command is now the wiring only:
//
//	manifest (data)  -> renderbatch.Prepare (typed plans, compiled up front)
//	                 -> chronon renderer (CLI client or warm daemon pool)
//	                 -> internal/media verification
//	                 -> internal/drive publication
//
// Everything it needs is a flag or a manifest field. Failures are collected per
// job and reported at the end; nothing calls log.Fatalf from a worker goroutine,
// because exiting there would abandon the warm daemons (and their GPU device)
// that the pool owns.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"mime"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

// options is the command's resolved configuration.
type options struct {
	manifestPath  string
	outDir        string
	dryRun        bool
	concurrency   int
	chrononBinary string
	chrononHome   string
	assetsRoot    string
	backend       string
	hardware      string
	encodePreset  string
	socketBase    string
	gpuDevice     uint
	daemons       int
	daemonLanes   int
	upload        bool
	publisher     string
	folder        string
	credentials   string
	token         string
	mockDir       string
	timeout       time.Duration
}

func main() {
	opts := parseFlags()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts); err != nil {
		log.Fatalf("batch-render-presets: %v", err)
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.manifestPath, "manifest", "", "path to the preset-render manifest JSON (required)")
	flag.StringVar(&opts.outDir, "out-dir", "", "override the manifest's output root; empty uses it, then the working directory")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "compile every plan, write the plan files and stop")
	flag.IntVar(&opts.concurrency, "concurrency", 3, "concurrent renders")
	// The CLI path comes from the setting, then CHRONON_BINARY, then the
	// chronon-home prefix. No developer-machine path is baked in.
	flag.StringVar(&opts.chrononBinary, "chronon-bin", "", "chronon3d_cli path (falls back to CHRONON_BINARY, then -chronon-home)")
	flag.StringVar(&opts.chrononHome, "chronon-home", "", "Chronon install prefix used to locate the CLI when -chronon-bin is empty")
	flag.StringVar(&opts.assetsRoot, "assets-root", "", "asset root passed to the renderer; empty uses the manifest's")
	flag.StringVar(&opts.backend, "backend", "vulkan", "render backend: vulkan | software")
	flag.StringVar(&opts.hardware, "hardware", chronon.DefaultHardwareEncoder, "hardware encoder: nvenc | none")
	flag.StringVar(&opts.encodePreset, "encode-preset", "p1", "encode preset (native: p1..p7; software: an x264 preset such as ultrafast)")
	flag.StringVar(&opts.socketBase, "socket", "", "render through warm Chronon3d daemons on this UNIX socket base; empty runs one CLI process per job")
	flag.UintVar(&opts.gpuDevice, "gpu-device", 0, "Vulkan device index for the daemons started by -socket")
	flag.IntVar(&opts.daemons, "daemons", 3, "warm daemons to spread jobs across, one socket each")
	flag.IntVar(&opts.daemonLanes, "daemon-lanes", 1, "concurrent RENDER_JOBs per daemon")
	flag.BoolVar(&opts.upload, "upload", false, "upload the rendered videos")
	flag.StringVar(&opts.publisher, "publisher", "oauth", "upload publisher: oauth | service-account | mock")
	flag.StringVar(&opts.folder, "folder", "", "Drive parent folder id (required to upload)")
	flag.StringVar(&opts.credentials, "credentials", "", "Drive credentials JSON (required for oauth/service-account)")
	flag.StringVar(&opts.token, "token", "", "Drive OAuth token JSON (required for oauth)")
	flag.StringVar(&opts.mockDir, "mock-dir", "", "destination directory for the mock publisher")
	flag.DurationVar(&opts.timeout, "timeout", 10*time.Minute, "per-render budget")
	flag.Parse()

	// The software lane only accepts libx264 presets; the native lane's default
	// is an NVENC tier. Keep the default convenient for both without letting a
	// caller's explicit preset be rewritten.
	if opts.hardware == chronon.HardwareEncoderNone && opts.encodePreset == "p1" {
		opts.encodePreset = "ultrafast"
	}
	return opts
}

// jobOutcome is one job's result, collected from the render pool.
type jobOutcome struct {
	job         renderbatch.Job
	renderMS    int64
	verifyMS    int64
	outputBytes int64
	renderErr   error
	verifyErr   error
	uploadErr   error
	uploadRef   string
}

func (o jobOutcome) failed() bool {
	return o.renderErr != nil || o.verifyErr != nil || o.uploadErr != nil
}

func run(ctx context.Context, opts options) error {
	if opts.manifestPath == "" {
		return fmt.Errorf("-manifest is required")
	}
	raw, err := os.ReadFile(opts.manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	manifest, err := renderbatch.Decode(raw)
	if err != nil {
		return err
	}

	// Roots resolve against the MANIFEST's directory so the batch is reproducible
	// from any working directory; an explicit flag is taken as given.
	roots, err := manifest.ResolveRoots(opts.manifestPath, opts.outDir, opts.assetsRoot)
	if err != nil {
		return err
	}
	outputRoot, assetsRoot := roots.Output, roots.Assets
	if info, err := os.Stat(assetsRoot); err != nil || !info.IsDir() {
		return fmt.Errorf("assets root %q is not a readable directory", assetsRoot)
	}

	// Compile the WHOLE matrix before rendering anything: a typo anywhere in the
	// manifest is a load error naming the job, not a failure on job 17 after the
	// first sixteen renders were paid for.
	prepared, err := manifest.Prepare(outputRoot)
	if err != nil {
		return err
	}
	log.Printf("manifest %s: %d job(s), canvas %dx%d @ %d/%d fps, output root %s",
		opts.manifestPath, len(prepared), manifest.Canvas.Width, manifest.Canvas.Height,
		manifest.Canvas.FPSNum, manifest.Canvas.FPSDen, outputRoot)

	for _, p := range prepared {
		if err := writePlan(p); err != nil {
			return err
		}
		log.Printf("plan %s: job=%s preset=%s motion=%s digest=%s frames=%d",
			p.PlanPath, p.Job.ID, p.Job.PresetID, p.Job.MotionID, p.PlanDigest[:12], p.Expect.Frames)
	}
	if opts.dryRun {
		log.Printf("[dry-run] %d plan(s) compiled and written; nothing rendered", len(prepared))
		return nil
	}

	renderer, releaseRenderer, err := buildRenderer(ctx, opts, assetsRoot)
	if err != nil {
		return err
	}
	defer releaseRenderer()

	publisher, err := buildPublisher(ctx, opts)
	if err != nil {
		return err
	}

	outcomes := renderAll(ctx, opts, prepared, assetsRoot, renderer, publisher)
	return report(outcomes)
}

// writePlan materializes one prepared plan beside its output.
func writePlan(p renderbatch.PreparedJob) error {
	if err := os.MkdirAll(filepath.Dir(p.PlanPath), 0o755); err != nil {
		return fmt.Errorf("create plan directory for %s: %w", p.Job.ID, err)
	}
	if err := os.WriteFile(p.PlanPath, p.Plan, 0o644); err != nil {
		return fmt.Errorf("write plan for %s: %w", p.Job.ID, err)
	}
	return nil
}

// buildRenderer returns the renderer plus the function that releases it.
//
// Both transports are the canonical ones from the chronon package: the CLI path
// is chronon.Client (stall watchdog, progress parsing, one process per render)
// and the warm path is the daemon pool. The command previously hand-built CLI
// flags for the first case, which bypassed every one of those behaviours.
func buildRenderer(ctx context.Context, opts options, assetsRoot string) (chronon.Renderer, func(), error) {
	if strings.TrimSpace(opts.socketBase) == "" {
		client := &chronon.Client{
			Home:            opts.chrononHome,
			BinaryPath:      opts.chrononBinary,
			Backend:         opts.backend,
			HardwareEncoder: opts.hardware,
		}
		log.Printf("transport: chronon3d_cli per job (binary=%s)", client.Binary())
		return client, func() {}, nil
	}
	pool, err := chronon.StartDaemonPool(ctx, chronon.DaemonOptions{
		SocketBase:     opts.socketBase,
		Count:          opts.daemons,
		LanesPerDaemon: opts.daemonLanes,
		Binary:         chrononBinaryPath(opts),
		AssetsRoot:     assetsRoot,
		Backend:        opts.backend,
		GPUDevice:      opts.gpuDevice,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
	})
	if err != nil {
		return nil, nil, err
	}
	sockets := pool.Sockets()
	log.Printf("transport: %d warm daemon(s) (%d owned) on %s..%s, %d lane(s) each",
		len(sockets), pool.OwnedCount(), sockets[0], sockets[len(sockets)-1], opts.daemonLanes)
	// Shutdown is explicit and idempotent, so releasing the renderer on the
	// error path below cannot leave a daemon holding the GPU device.
	return pool, func() { pool.Shutdown(context.Background()) }, nil
}

// chrononBinaryPath resolves the binary for the daemon pool, which needs a path
// up front (the CLI client can resolve it lazily).
func chrononBinaryPath(opts options) string {
	if opts.chrononBinary != "" {
		return opts.chrononBinary
	}
	if override := os.Getenv("CHRONON_BINARY"); override != "" {
		return override
	}
	if opts.chrononHome != "" {
		return filepath.Join(opts.chrononHome, "bin", "chronon3d_cli")
	}
	return "chronon3d_cli"
}

// buildPublisher constructs the publication sink, or nil when uploads are off.
func buildPublisher(ctx context.Context, opts options) (drive.Publisher, error) {
	if !opts.upload {
		return nil, nil
	}
	switch opts.publisher {
	case "mock":
		if opts.mockDir == "" {
			return nil, fmt.Errorf("-mock-dir is required for the mock publisher")
		}
		return drive.NewMock(opts.mockDir, 0), nil
	case "oauth":
		if opts.credentials == "" || opts.token == "" {
			return nil, fmt.Errorf("-credentials and -token are required for the oauth publisher")
		}
		if opts.folder == "" {
			return nil, fmt.Errorf("-folder is required to upload")
		}
		return drive.NewGoogleOAuth(ctx, opts.credentials, opts.token, opts.folder)
	case "service-account":
		if opts.credentials == "" {
			return nil, fmt.Errorf("-credentials is required for the service-account publisher")
		}
		if opts.folder == "" {
			return nil, fmt.Errorf("-folder is required to upload")
		}
		return drive.NewGoogle(ctx, opts.credentials, opts.folder)
	default:
		return nil, fmt.Errorf("-publisher must be oauth, service-account or mock, got %q", opts.publisher)
	}
}

// renderAll runs the matrix on a fixed-size pool and returns every job's outcome
// in manifest order.
//
// A failed job never stops its peers: this is a render farm, and one bad preset
// must not throw away the renders that already succeeded.
func renderAll(ctx context.Context, opts options, prepared []renderbatch.PreparedJob, assetsRoot string, renderer chronon.Renderer, publisher drive.Publisher) []jobOutcome {
	outcomes := make([]jobOutcome, len(prepared))
	workers := opts.concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(prepared) {
		workers = len(prepared)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var done int64
	var progressMu sync.Mutex

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				p := prepared[index]
				outcome := renderOne(ctx, opts, p, assetsRoot, renderer, publisher)
				outcomes[index] = outcome
				progressMu.Lock()
				done++
				position := done
				progressMu.Unlock()
				logJobOutcome(int(position), len(prepared), outcome)
			}
		}()
	}
	for i := range prepared {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return outcomes
}

// renderOne renders, verifies and (optionally) publishes one job.
func renderOne(ctx context.Context, opts options, p renderbatch.PreparedJob, assetsRoot string, renderer chronon.Renderer, publisher drive.Publisher) jobOutcome {
	outcome := jobOutcome{job: p.Job}
	gpuRequired := opts.hardware != "" && opts.hardware != chronon.HardwareEncoderNone

	req := chronon.RenderRequest{
		PlanPath:        p.PlanPath,
		AssetsRoot:      assetsRoot,
		OutputPath:      p.OutputPath,
		EncodePreset:    opts.encodePreset,
		HardwareEncoder: opts.hardware,
		TotalFrames:     int64(p.Expect.Frames),
		Requirements: chronon.ExecutionRequirements{
			Backend:     opts.backend,
			GPURequired: gpuRequired,
			// The engine's own hot-path mode, matching what the CLI transport
			// emits for this workload.
			CPUFallbackAllowed: true,
		},
	}
	if !gpuRequired {
		// No native encoder requested: the host pipe lane carries the frame and
		// libx264 rejects the NVENC-only pN presets, so a non-native pixel
		// format is used.
		req.Output.PipePixFmt = "rgba"
	}

	renderCtx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	renderStart := time.Now()
	if err := renderer.Render(renderCtx, req); err != nil {
		outcome.renderErr = err
		return outcome
	}
	outcome.renderMS = time.Since(renderStart).Milliseconds()

	verifyStart := time.Now()
	outcome.verifyErr = verifyRender(renderCtx, p)
	outcome.verifyMS = time.Since(verifyStart).Milliseconds()
	if outcome.verifyErr != nil {
		return outcome
	}

	if info, err := os.Stat(p.OutputPath); err == nil {
		outcome.outputBytes = info.Size()
	}
	if publisher == nil {
		return outcome
	}
	ref, err := publish(ctx, publisher, p, opts.folder)
	outcome.uploadRef, outcome.uploadErr = ref, err
	return outcome
}

// verifyRender certifies one rendered artifact against the job's DERIVED media
// contract and then decodes it end to end.
//
// Both checks come from internal/media, which is the same certification the
// production worker applies: the previous command re-implemented the structural
// half by parsing `ffprobe -of csv` by hand with a hardcoded expectation.
func verifyRender(ctx context.Context, p renderbatch.PreparedJob) error {
	probe, err := media.ProbeFile(ctx, p.OutputPath)
	if err != nil {
		return err
	}
	expect := p.Expect
	if probe.FrameCount != expect.Frames {
		return fmt.Errorf("frames: probed %d, want %d", probe.FrameCount, expect.Frames)
	}
	if probe.Width != expect.Width || probe.Height != expect.Height {
		return fmt.Errorf("resolution: probed %dx%d, want %dx%d", probe.Width, probe.Height, expect.Width, expect.Height)
	}
	if probe.FPSNum*expect.FPSDen != expect.FPSNum*probe.FPSDen {
		return fmt.Errorf("fps: probed %d/%d, want %d/%d", probe.FPSNum, probe.FPSDen, expect.FPSNum, expect.FPSDen)
	}
	return media.ValidateDecode(ctx, p.OutputPath)
}

// publish uploads one artifact and returns the provider reference.
func publish(ctx context.Context, publisher drive.Publisher, p renderbatch.PreparedJob, folder string) (string, error) {
	name := filepath.Base(p.OutputPath)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	parent := folder
	if publisherIsMock(publisher) {
		// The mock publisher writes into its own directory and takes no folder
		// id; passing one would only mask a missing -folder in the real modes.
		parent = ""
	}
	result, err := publisher.Publish(ctx, drive.PublishRequest{
		Name:         name,
		ContentType:  contentType,
		Path:         p.OutputPath,
		ParentFolder: parent,
	})
	if err != nil {
		return "", err
	}
	if result.WebViewLink != "" {
		return result.WebViewLink, nil
	}
	return result.FileID, nil
}

// publisherIsMock reports whether the publisher is the in-process mock.
func publisherIsMock(publisher drive.Publisher) bool {
	_, ok := publisher.(*drive.Mock)
	return ok
}

// logJobOutcome reports one job as it finishes, in the same shape for every
// terminal state so a log grep sees failures and successes together.
func logJobOutcome(position, total int, outcome jobOutcome) {
	switch {
	case outcome.renderErr != nil:
		log.Printf("[%d/%d] FAIL render %s: %v", position, total, outcome.job.ID, outcome.renderErr)
	case outcome.verifyErr != nil:
		log.Printf("[%d/%d] FAIL verify %s: %v", position, total, outcome.job.ID, outcome.verifyErr)
	case outcome.uploadErr != nil:
		log.Printf("[%d/%d] FAIL upload %s: %v", position, total, outcome.job.ID, outcome.uploadErr)
	default:
		log.Printf("[%d/%d] OK %s render=%dms verify=%dms bytes=%d%s",
			position, total, outcome.job.ID, outcome.renderMS, outcome.verifyMS, outcome.outputBytes, uploadSuffix(outcome.uploadRef))
	}
}

func uploadSuffix(ref string) string {
	if ref == "" {
		return ""
	}
	return " uploaded=" + ref
}

// report prints the batch summary and turns a non-empty failure set into an
// error, which main logs after every deferred release has already run.
func report(outcomes []jobOutcome) error {
	var failed []string
	var rendered int
	for _, outcome := range outcomes {
		if outcome.failed() {
			failed = append(failed, outcome.job.ID)
			continue
		}
		rendered++
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d/%d job(s) failed: %s", len(failed), len(outcomes), strings.Join(failed, ", "))
	}
	log.Printf("done: %d/%d job(s) rendered, verified and published", rendered, len(outcomes))
	return nil
}
