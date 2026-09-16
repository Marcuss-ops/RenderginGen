// pool.go owns the LABORATORY harness that answers a question the production
// probe cannot: do TWO warm render runtimes of the same shape fit on this device
// at the same time?
//
// Why it exists. The single-job probe (probe.go) submits through the normal
// queue path, i.e. through the daemon that serializes RENDER_JOB behind
// `m_render_job_mutex`. While that mutex is held, exactly one job is resident and
// executed, so the delta it measures is the incremental cost of ONE active job —
// it cannot prove that a second one coexists, and the two-runtime number that
// was recorded separately (two cold-start CLI processes) is not the same object:
// a CLI process allocates for one plan and exits, while a daemon keeps its pools,
// caches and atlases resident and charges VRAM per plan at admission.
//
// This harness therefore holds TWO owned daemons (two processes, two runtimes,
// two warm pools) on distinct sockets, renders one job on each CONCURRENTLY, and
// samples the device across five labelled phases — idle, warm pools, rendering,
// resident hold (pools alive, nothing rendering) and after shutdown — so the
// incremental cost of the second runtime is separable from the in-flight cost of
// a render and from the steady-state residency of a warm pool.
//
// What it does NOT do: it never touches production semantics, it never removes
// `m_render_job_mutex`, and it reports
// `authorizes_mutex_removal: false` unconditionally. Feeding
// `derive_vram_working_set` is a calibration decision that needs this number on
// several shapes (overlay-only, source-video FullGraph, DirectYUV, 4K) plus
// internal allocator telemetry; one shape on one host is evidence, not a gate.
package vramprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

// PoolReportSchema identifies the two-runtime pool report.
const PoolReportSchema = "renderinggen.vram-two-runtime-pool-report.v1"

// poolPhase names one labelled window of the measurement. The phases exist so a
// reader can separate "the second runtime is resident" from "a render is in
// flight": those are different costs and conflating them is what made the first
// probe's number ambiguous.
type poolPhase string

const (
	phaseIdle         poolPhase = "idle"
	phaseWarmPools    poolPhase = "warm_pools"
	phaseRendering    poolPhase = "rendering"
	phaseResidentHold poolPhase = "resident_hold"
	phaseAfterStop    poolPhase = "after_shutdown"
)

// PoolOptions configures one two-runtime measurement.
type PoolOptions struct {
	// PlanPath is the CONCRETE render plan (chronon.render-plan.v2) both jobs
	// render. The harness refuses a semantic plan: the daemon cannot render it.
	PlanPath string
	// OutputDir receives the two artifacts and the samples log.
	OutputDir string
	// AssetsRoot is the daemon's asset root (-a).
	AssetsRoot string
	// Binary is the chronon3d_cli executable the owned daemons run.
	Binary string
	// Backend is the render backend (default vulkan).
	Backend string
	// HardwareEncoder is the native encoder (default nvenc; "none" for the
	// software lane).
	HardwareEncoder string
	// EncodePreset is the explicit native encode preset (default p1).
	EncodePreset string
	// GPUDevice is the Vulkan device index.
	GPUDevice uint
	// Daemons is how many runtimes run CONCURRENTLY. It defaults to 2 and is the
	// whole point of the harness, so values below 2 are rejected.
	Daemons int
	// LanesPerDaemon caps concurrent RENDER_JOBs per daemon; 1 is the production
	// lane today and keeps each job on its own runtime.
	LanesPerDaemon int
	// Interval is the nvidia-smi sampling period. It defaults to 20 ms and is
	// deliberately finer than the 50 ms used by the first probe, because the
	// interesting signal is a short peak during admission.
	Interval time.Duration
	// Hold is how long the two warm pools stay alive with nothing rendering
	// after the jobs finish. It is what separates a pool's RESIDENT cost from
	// a render's in-flight cost.
	Hold time.Duration
	// Timeout bounds one render.
	Timeout time.Duration
	// ReportPath is the JSON report to write (required).
	ReportPath string
	// SamplesPath is where the raw samples are written; default: beside the
	// report as <report>.samples.log.
	SamplesPath string
	Logger      *log.Logger
	// Stdout and Stderr receive the owned daemons' output. They default to
	// os.Stderr: a harness whose daemons print why they refused to start must
	// not discard the reason.
	Stdout io.Writer
	Stderr io.Writer
}

// PoolReport is the document of record for one two-runtime measurement.
type PoolReport struct {
	Schema string `json:"schema"`

	PlanPath   string      `json:"plan_path"`
	PlanSHA256 string      `json:"plan_sha256"`
	Shape      PoolShape   `json:"shape"`
	Device     PoolDevice  `json:"device"`
	Execution  PoolRuntime `json:"execution"`

	DaemonCount      int     `json:"daemon_count"`
	OwnedDaemons     int     `json:"owned_daemons"`
	LanesPerDaemon   int     `json:"lanes_per_daemon"`
	SampleIntervalMS int     `json:"sample_interval_ms"`
	HoldSeconds      float64 `json:"hold_seconds"`

	// Headline numbers, so the document is readable without knowing the phase
	// vocabulary. Every delta is against the idle phase of THIS run, because a
	// device shared with other tenants drifts between runs.
	IdleDeviceMiB             int  `json:"idle_device_mib"`
	WarmPoolsDeviceMiB        int  `json:"warm_pools_device_mib"`
	RenderingPeakDeviceMiB    int  `json:"rendering_peak_device_mib"`
	RenderingDeltaMiB         int  `json:"rendering_delta_mib"`
	ResidentHoldDeviceMiB     int  `json:"resident_hold_device_mib"`
	SecondRuntimeResidentMiB  int  `json:"second_runtime_resident_mib"`
	IdleResidentDaemonMiB     int  `json:"idle_resident_daemon_mib"`
	BaselineHasResidentDaemon bool `json:"baseline_has_resident_daemon"`
	// WarmPoolsTouchedDevice is false when the two fresh daemons had allocated
	// nothing yet during the warm phase: their engines allocate lazily on first
	// admission, so a warm pool that is merely listening costs ~0 MiB and the
	// RESIDENT cost is read from the resident-hold phase instead.
	WarmPoolsTouchedDevice bool `json:"warm_pools_touched_device"`

	Phases      map[string]PoolPhaseFacts `json:"phases"`
	Samples     int                       `json:"samples"`
	SamplesFile string                    `json:"samples_file"`

	Jobs               []PoolJobFacts `json:"jobs"`
	ArtifactsIdentical bool           `json:"artifacts_identical"`

	// AuthorizesMutexRemoval is ALWAYS false: it is stated in the document so no
	// consumer can mistake a single-shape laboratory number for the calibration
	// that would license a production concurrency change.
	AuthorizesMutexRemoval bool   `json:"authorizes_mutex_removal"`
	Caveat                 string `json:"caveat"`
}

// PoolShape is the render shape both jobs share, read from the plan itself.
//
// LayerTypes is recorded as a FACT rather than reduced to a lane label: which
// lane a plan lands on is the engine's decision, and a harness that guessed it
// from a hardcoded flag would attach a confident label to a number measured on
// something else. The overlay shapes the calibration cares about are the ones
// whose layer types are colour/text/image only.
type PoolShape struct {
	Width        int      `json:"width"`
	Height       int      `json:"height"`
	FPSNum       int      `json:"fps_num"`
	FPSDen       int      `json:"fps_den"`
	Frames       int      `json:"frames"`
	Codec        string   `json:"codec"`
	OutputFormat string   `json:"output_format"`
	LayerTypes   []string `json:"layer_types"`
}

// PoolDevice records the hardware the number belongs to.
type PoolDevice struct {
	Name          string `json:"name"`
	DriverVersion string `json:"driver_version"`
	TotalMiB      int    `json:"total_mib"`
}

// PoolRuntime records how the two runtimes were launched, because a VRAM number
// without the transport it was measured on is not comparable to another one.
type PoolRuntime struct {
	Binary          string `json:"binary"`
	Backend         string `json:"backend"`
	HardwareEncoder string `json:"hardware_encoder"`
	EncodePreset    string `json:"encode_preset"`
	SocketBase      string `json:"socket_base"`
	GPUDevice       uint   `json:"gpu_device"`
}

// PoolPhaseFacts is the memory picture inside one labelled window.
type PoolPhaseFacts struct {
	Samples         int `json:"samples"`
	IdleDeviceMiB   int `json:"idle_device_mib"`
	PeakDeviceMiB   int `json:"peak_device_mib"`
	DeviceDeltaMiB  int `json:"device_delta_mib"`
	PeakDaemonMiB   int `json:"peak_daemon_mib"`
	IdleDaemonMiB   int `json:"idle_daemon_mib"`
	DaemonDeltaMiB  int `json:"daemon_delta_mib"`
	PeakUtilization int `json:"peak_utilization_pct"`
}

// PoolJobFacts is one rendered artifact.
type PoolJobFacts struct {
	Index      int    `json:"index"`
	Socket     string `json:"socket"`
	Output     string `json:"output"`
	RenderMS   int64  `json:"render_ms"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	FailedWith string `json:"failed_with,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	WallMS     int64  `json:"wall_ms"`
}

// poolSample is one nvidia-smi observation, tagged with the phase it fell in.
type poolSample struct {
	phase       poolPhase
	at          time.Duration
	device      int
	daemon      int
	utilization int
}

// RunTwoRuntimePool starts the configured number of OWNED daemons, renders one
// job on each concurrently and reports what the device did.
//
// Every step that could make the number meaningless fails loudly instead of
// being reported: a pool that could not own its sockets (a stale socket file
// means another process already serves it), a plan that is not the concrete
// chronon plan, a job that failed, or a device that cannot be sampled.
func RunTwoRuntimePool(ctx context.Context, opts PoolOptions) (*PoolReport, error) {
	if opts.PlanPath == "" {
		return nil, fmt.Errorf("vramprobe: -plan is required")
	}
	if opts.ReportPath == "" {
		return nil, fmt.Errorf("vramprobe: -out is required")
	}
	if opts.Daemons < 2 {
		return nil, fmt.Errorf("vramprobe: the two-runtime harness needs at least 2 daemons, got %d", opts.Daemons)
	}
	if opts.Binary == "" {
		return nil, fmt.Errorf("vramprobe: -chronon-bin is required (the owned daemons need an executable)")
	}
	if opts.Backend == "" {
		opts.Backend = "vulkan"
	}
	if opts.HardwareEncoder == "" {
		opts.HardwareEncoder = "nvenc"
	}
	if opts.EncodePreset == "" {
		opts.EncodePreset = "p1"
	}
	if opts.LanesPerDaemon <= 0 {
		opts.LanesPerDaemon = 1
	}
	if opts.Interval <= 0 {
		opts.Interval = 20 * time.Millisecond
	}
	if opts.Hold <= 0 {
		opts.Hold = 15 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Dir(opts.ReportPath)
	}
	if opts.SamplesPath == "" {
		opts.SamplesPath = opts.ReportPath + ".samples.log"
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "vram-two-runtime: ", log.LstdFlags)
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stderr
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	// Every path handed to a daemon or recorded as evidence is absolutized here.
	// The engine's AssetResolver rejects a relative asset root outright ("root
	// must be an absolute path"), and a report that names a path relative to
	// whatever directory the harness happened to run in is not reproducible.
	if err := absolutizePoolPaths(&opts); err != nil {
		return nil, err
	}
	if info, err := os.Stat(opts.AssetsRoot); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("vramprobe: assets root %q is not a readable directory", opts.AssetsRoot)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("vramprobe: create output dir: %w", err)
	}

	shape, planDigest, err := readPoolPlan(opts.PlanPath)
	if err != nil {
		return nil, err
	}
	device, err := poolDeviceInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("vramprobe: nvidia-smi: %w", err)
	}

	// A fresh socket base, so a stale socket file can never make the harness
	// render through a daemon it does not own: StartDaemonPool reuses a socket
	// that already exists, which is right for production and fatal for a
	// measurement of "two runtimes I started".
	socketDir, err := os.MkdirTemp("", "vram-two-runtime-")
	if err != nil {
		return nil, fmt.Errorf("vramprobe: create socket dir: %w", err)
	}
	defer os.RemoveAll(socketDir)
	socketBase := filepath.Join(socketDir, "chronon.sock")

	collector := newPoolSampler(ctx, opts.Interval)
	collector.setPhase(phaseIdle)
	// Settle the idle baseline before any daemon exists: this phase is what every
	// later delta is read against, and it must not contain this harness's own
	// start-up.
	collector.snapshot()
	time.Sleep(4 * opts.Interval)

	pool, err := chronon.StartDaemonPool(ctx, chronon.DaemonOptions{
		SocketBase:     socketBase,
		Count:          opts.Daemons,
		LanesPerDaemon: opts.LanesPerDaemon,
		Binary:         opts.Binary,
		AssetsRoot:     opts.AssetsRoot,
		Backend:        opts.Backend,
		GPUDevice:      opts.GPUDevice,
		Stdout:         opts.Stdout,
		Stderr:         opts.Stderr,
	})
	if err != nil {
		collector.stop()
		return nil, fmt.Errorf("vramprobe: start daemon pool: %w", err)
	}
	owned := pool.OwnedCount()
	if owned != opts.Daemons {
		pool.Shutdown(context.Background())
		collector.stop()
		return nil, fmt.Errorf("vramprobe: %d of %d daemons were OWNED (the rest were already served); the measurement would not be two fresh runtimes", owned, opts.Daemons)
	}
	sockets := pool.Sockets()
	logger.Printf("%d owned daemon(s) on %s..%s, %d lane(s) each", owned, sockets[0], sockets[len(sockets)-1], opts.LanesPerDaemon)

	// Warm pools: both runtimes are resident and idle. This is the phase that
	// answers "what does a second warm runtime cost while nothing renders".
	collector.setPhase(phaseWarmPools)
	time.Sleep(3 * time.Second)

	collector.setPhase(phaseRendering)
	jobs := runPoolJobs(ctx, opts, pool, sockets, shape, logger)

	collector.setPhase(phaseResidentHold)
	time.Sleep(opts.Hold)

	pool.Shutdown(context.Background())
	collector.setPhase(phaseAfterStop)
	time.Sleep(3 * time.Second)
	collector.stop()

	report := &PoolReport{
		Schema:                 PoolReportSchema,
		PlanPath:               opts.PlanPath,
		PlanSHA256:             planDigest,
		Shape:                  shape,
		Device:                 device,
		Execution:              PoolRuntime{Binary: opts.Binary, Backend: opts.Backend, HardwareEncoder: opts.HardwareEncoder, EncodePreset: opts.EncodePreset, SocketBase: socketBase, GPUDevice: opts.GPUDevice},
		DaemonCount:            opts.Daemons,
		OwnedDaemons:           owned,
		LanesPerDaemon:         opts.LanesPerDaemon,
		SampleIntervalMS:       int(opts.Interval / time.Millisecond),
		HoldSeconds:            opts.Hold.Seconds(),
		Phases:                 collector.phaseFacts(),
		Samples:                collector.count(),
		SamplesFile:            opts.SamplesPath,
		Jobs:                   jobs,
		ArtifactsIdentical:     jobsIdentical(jobs),
		AuthorizesMutexRemoval: false,
		Caveat: "laboratory harness: two OWNED warm daemons, one concurrent job each, one plan shape on one host. " +
			"It falsifies a per-job VRAM calibration for this shape and measures the second runtime's resident cost; " +
			"it does NOT authorize removing m_render_job_mutex (that needs several certified shapes plus internal allocator telemetry).",
	}
	// Headline numbers. The per-runtime figure is derived, and the report says
	// how: this host keeps one chronon3d_cli daemon resident (the systemd unit),
	// so the idle phase already contains one warm runtime and the warm-pools
	// phase contains three. Dividing the increment by the number of ADDED
	// daemons is therefore the resident cost of one extra runtime — not the cost
	// of a render, and not a license to raise concurrency.
	//
	// The increment is clamped at zero because a fresh daemon allocates its
	// device memory lazily: at the moment the warm phase is sampled the engine may
	// have touched nothing at all, and a negative "cost" would be an artefact of
	// the sample timing rather than a measurement. When that happens the flag says
	// so and the resident-hold phase is the number to read.
	report.IdleDeviceMiB = report.Phases[string(phaseIdle)].PeakDeviceMiB
	report.WarmPoolsDeviceMiB = report.Phases[string(phaseWarmPools)].PeakDeviceMiB
	report.RenderingPeakDeviceMiB = report.Phases[string(phaseRendering)].PeakDeviceMiB
	report.RenderingDeltaMiB = report.RenderingPeakDeviceMiB - report.IdleDeviceMiB
	report.ResidentHoldDeviceMiB = report.Phases[string(phaseResidentHold)].PeakDeviceMiB
	report.IdleResidentDaemonMiB = report.Phases[string(phaseIdle)].PeakDaemonMiB
	report.BaselineHasResidentDaemon = report.IdleResidentDaemonMiB > 0
	warmGrowth := report.WarmPoolsDeviceMiB - report.IdleDeviceMiB
	report.WarmPoolsTouchedDevice = warmGrowth > 0
	if warmGrowth < 0 {
		warmGrowth = 0
	}
	report.SecondRuntimeResidentMiB = warmGrowth / opts.Daemons

	if err := collector.writeSamples(opts.SamplesPath); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(opts.ReportPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(opts.ReportPath, append(raw, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("vramprobe: write report: %w", err)
	}
	logger.Printf("phases: idle %d MiB -> warm pools %d -> rendering %d -> resident hold %d -> after shutdown %d",
		report.IdleDeviceMiB, report.WarmPoolsDeviceMiB, report.RenderingPeakDeviceMiB,
		report.ResidentHoldDeviceMiB, report.Phases[string(phaseAfterStop)].PeakDeviceMiB)
	logger.Printf("derived: rendering delta vs idle %d MiB, second warm runtime resident ~%d MiB (%d added daemons, idle baseline daemon %d MiB)",
		report.RenderingDeltaMiB, report.SecondRuntimeResidentMiB, opts.Daemons, report.IdleResidentDaemonMiB)
	return report, nil
}

// runPoolJobs submits one job per runtime CONCURRENTLY and waits for both.
//
// The two goroutines start behind a barrier so the renders genuinely overlap —
// a sequential loop would measure one runtime twice and hide the whole point.
func runPoolJobs(ctx context.Context, opts PoolOptions, pool *chronon.DaemonPool, sockets []string, shape PoolShape, logger *log.Logger) []PoolJobFacts {
	jobs := make([]PoolJobFacts, opts.Daemons)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < opts.Daemons; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			output := filepath.Join(opts.OutputDir, fmt.Sprintf("two-runtime-job-%d.mp4", index))
			facts := PoolJobFacts{Index: index, Output: output, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			if index < len(sockets) {
				facts.Socket = sockets[index]
			}
			<-start
			renderCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
			request := chronon.RenderRequest{
				PlanPath:        opts.PlanPath,
				AssetsRoot:      opts.AssetsRoot,
				OutputPath:      output,
				EncodePreset:    opts.EncodePreset,
				HardwareEncoder: opts.HardwareEncoder,
				TotalFrames:     int64(shape.Frames),
				Requirements: chronon.ExecutionRequirements{
					Backend:            opts.Backend,
					GPURequired:        opts.HardwareEncoder != "" && opts.HardwareEncoder != chronon.HardwareEncoderNone,
					CPUFallbackAllowed: true,
				},
			}
			began := time.Now()
			err := pool.Render(renderCtx, request)
			facts.RenderMS = time.Since(began).Milliseconds()
			facts.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			facts.WallMS = facts.RenderMS
			if err != nil {
				facts.FailedWith = err.Error()
				logger.Printf("job %d on %s FAILED: %v", index, facts.Socket, err)
				jobs[index] = facts
				return
			}
			if info, statErr := os.Stat(output); statErr == nil {
				facts.Bytes = info.Size()
			}
			facts.SHA256 = fileSHA256(output)
			logger.Printf("job %d on %s done in %d ms (%d bytes, sha %s)", index, facts.Socket, facts.RenderMS, facts.Bytes, firstN(facts.SHA256, 12))
			jobs[index] = facts
		}(i)
	}
	close(start)
	wg.Wait()
	return jobs
}

// jobsIdentical reports whether every job produced the same bytes. Same plan,
// same host, same encoder: identical output is what makes the two-runtime number
// comparable to a single-runtime render instead of a different workload.
func jobsIdentical(jobs []PoolJobFacts) bool {
	reference := ""
	for _, job := range jobs {
		if job.FailedWith != "" || job.SHA256 == "" {
			return false
		}
		if reference == "" {
			reference = job.SHA256
			continue
		}
		if job.SHA256 != reference {
			return false
		}
	}
	return reference != ""
}

// absolutizePoolPaths resolves every input and output path the run depends on.
func absolutizePoolPaths(opts *PoolOptions) error {
	fields := []struct {
		name  string
		value *string
	}{
		{"plan", &opts.PlanPath},
		{"assets root", &opts.AssetsRoot},
		{"output dir", &opts.OutputDir},
		{"report", &opts.ReportPath},
		{"samples", &opts.SamplesPath},
		{"chronon binary", &opts.Binary},
	}
	for _, field := range fields {
		if *field.value == "" {
			continue
		}
		absolved, err := filepath.Abs(*field.value)
		if err != nil {
			return fmt.Errorf("vramprobe: resolve %s %q: %w", field.name, *field.value, err)
		}
		*field.value = absolved
	}
	return nil
}

// readPoolPlan reads the SHAPE out of the concrete render plan and hashes the
// document, so the evidence names exactly what was rendered.
func readPoolPlan(path string) (PoolShape, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return PoolShape{}, "", fmt.Errorf("vramprobe: read plan: %w", err)
	}
	var document struct {
		Schema string `json:"schema"`
		Canvas struct {
			Width          int `json:"width"`
			Height         int `json:"height"`
			FPSNum         int `json:"fps_num"`
			FPSDen         int `json:"fps_den"`
			DurationFrames int `json:"duration_frames"`
		} `json:"canvas"`
		Output struct {
			Format string `json:"format"`
			Codec  string `json:"codec"`
		} `json:"output"`
		Layers []struct {
			Type string `json:"type"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return PoolShape{}, "", fmt.Errorf("vramprobe: decode plan %s: %w", path, err)
	}
	// The schema constant is renderbatch's: it is the one declaration of the
	// concrete plan contract, so the harness cannot silently accept a document
	// the renderer would reject.
	if document.Schema != renderbatch.ChrononPlanSchema {
		return PoolShape{}, "", fmt.Errorf("vramprobe: plan %s declares schema %q, want the concrete %s (a semantic plan cannot be rendered)", path, document.Schema, renderbatch.ChrononPlanSchema)
	}
	if document.Canvas.Width <= 0 || document.Canvas.Height <= 0 || document.Canvas.DurationFrames <= 0 {
		return PoolShape{}, "", fmt.Errorf("vramprobe: plan %s has an unusable canvas %dx%d frames=%d", path, document.Canvas.Width, document.Canvas.Height, document.Canvas.DurationFrames)
	}
	digest := sha256.Sum256(raw)
	seen := map[string]bool{}
	types := make([]string, 0, len(document.Layers))
	for _, layer := range document.Layers {
		if layer.Type == "" || seen[layer.Type] {
			continue
		}
		seen[layer.Type] = true
		types = append(types, layer.Type)
	}
	sort.Strings(types)
	return PoolShape{
		Width:        document.Canvas.Width,
		Height:       document.Canvas.Height,
		FPSNum:       document.Canvas.FPSNum,
		FPSDen:       document.Canvas.FPSDen,
		Frames:       document.Canvas.DurationFrames,
		Codec:        document.Output.Codec,
		OutputFormat: document.Output.Format,
		LayerTypes:   types,
	}, hex.EncodeToString(digest[:]), nil
}

// poolDeviceInfo reads the device identity the number belongs to.
func poolDeviceInfo(ctx context.Context) (PoolDevice, error) {
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=name,driver_version,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return PoolDevice{}, err
	}
	fields := strings.Split(strings.TrimSpace(strings.Split(string(out), "\n")[0]), ",")
	if len(fields) < 3 {
		return PoolDevice{}, fmt.Errorf("unexpected nvidia-smi output %q", strings.TrimSpace(string(out)))
	}
	total, err := strconv.Atoi(strings.TrimSpace(fields[2]))
	if err != nil {
		return PoolDevice{}, err
	}
	return PoolDevice{
		Name:          strings.TrimSpace(fields[0]),
		DriverVersion: strings.TrimSpace(fields[1]),
		TotalMiB:      total,
	}, nil
}

// poolSampler samples nvidia-smi on a ticker and tags every observation with the
// phase that was active when it was taken.
//
// The phase is carried by an atomic string rather than by restarting the
// sampler per phase, so the transition itself cannot drop or duplicate samples
// around the interesting moment (a pool starting, a render beginning).
type poolSampler struct {
	interval time.Duration
	ctx      context.Context

	phaseMu sync.Mutex
	phase   poolPhase

	samplesMu sync.Mutex
	samples   []poolSample

	started time.Time
	stopCh  chan struct{}
	doneCh  chan struct{}
	once    sync.Once

	counter atomic.Int64
}

func newPoolSampler(ctx context.Context, interval time.Duration) *poolSampler {
	sampler := &poolSampler{
		interval: interval,
		ctx:      ctx,
		phase:    phaseIdle,
		started:  time.Now(),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	go sampler.loop()
	return sampler
}

func (s *poolSampler) loop() {
	defer close(s.doneCh)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			device, utilization, err := deviceSample(s.ctx)
			if err != nil {
				continue
			}
			entry := poolSample{
				phase:       s.currentPhase(),
				at:          time.Since(s.started),
				device:      device,
				daemon:      daemonMiB(s.ctx),
				utilization: utilization,
			}
			s.samplesMu.Lock()
			s.samples = append(s.samples, entry)
			s.samplesMu.Unlock()
			s.counter.Add(1)
		}
	}
}

func (s *poolSampler) setPhase(phase poolPhase) {
	s.phaseMu.Lock()
	s.phase = phase
	s.phaseMu.Unlock()
}

func (s *poolSampler) currentPhase() poolPhase {
	s.phaseMu.Lock()
	defer s.phaseMu.Unlock()
	return s.phase
}

// snapshot takes one immediate out-of-band observation (the idle baseline), so
// the baseline does not wait for a ticker edge.
func (s *poolSampler) snapshot() poolSample {
	device, utilization, err := deviceSample(s.ctx)
	entry := poolSample{phase: s.currentPhase(), at: time.Since(s.started), device: device, utilization: utilization}
	if err == nil {
		entry.daemon = daemonMiB(s.ctx)
	}
	s.samplesMu.Lock()
	s.samples = append(s.samples, entry)
	s.samplesMu.Unlock()
	s.counter.Add(1)
	return entry
}

func (s *poolSampler) stop() {
	s.once.Do(func() {
		close(s.stopCh)
		<-s.doneCh
	})
}

func (s *poolSampler) count() int {
	return int(s.counter.Load())
}

func (s *poolSampler) all() []poolSample {
	s.samplesMu.Lock()
	defer s.samplesMu.Unlock()
	out := make([]poolSample, len(s.samples))
	copy(out, s.samples)
	return out
}

// phaseFacts reduces the raw series to the per-phase numbers the report carries.
// The DELTA is always computed against the phase's own first sample: a device
// that is shared with other tenants drifts, so a global baseline would attribute
// someone else's growth to this harness.
func (s *poolSampler) phaseFacts() map[string]PoolPhaseFacts {
	facts := map[string]PoolPhaseFacts{}
	for _, entry := range s.all() {
		key := string(entry.phase)
		current, ok := facts[key]
		if !ok {
			current = PoolPhaseFacts{
				IdleDeviceMiB: entry.device,
				IdleDaemonMiB: entry.daemon,
			}
		}
		current.Samples++
		if entry.device > current.PeakDeviceMiB {
			current.PeakDeviceMiB = entry.device
		}
		if entry.daemon > current.PeakDaemonMiB {
			current.PeakDaemonMiB = entry.daemon
		}
		if entry.utilization > current.PeakUtilization {
			current.PeakUtilization = entry.utilization
		}
		current.DeviceDeltaMiB = current.PeakDeviceMiB - current.IdleDeviceMiB
		current.DaemonDeltaMiB = current.PeakDaemonMiB - current.IdleDaemonMiB
		facts[key] = current
	}
	return facts
}

// writeSamples writes the raw series, so a reader can re-derive any statistic
// instead of trusting the summary.
func (s *poolSampler) writeSamples(path string) error {
	var builder strings.Builder
	builder.WriteString("# phase elapsed_s device_used_mib chronon3d_cli_used_mib gpu_util_pct\n")
	for _, entry := range s.all() {
		fmt.Fprintf(&builder, "%s %.2f %d %d %d\n", entry.phase, entry.at.Seconds(), entry.device, entry.daemon, entry.utilization)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("vramprobe: create samples dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return fmt.Errorf("vramprobe: write samples: %w", err)
	}
	return nil
}

func fileSHA256(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return ""
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func firstN(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}
