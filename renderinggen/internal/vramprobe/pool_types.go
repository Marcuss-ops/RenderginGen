package vramprobe

import (
	"io"
	"log"
	"time"
)

// pool_types.go owns the two-runtime pool report contract: the options, the
// report and phase facts the probe publishes, and the shapes it describes.
// Behaviour lives in pool.go (the run), pool_sampler.go (sampling) and
// pool_jobs.go (per-job helpers).

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
	// phaseProbe is the single-job probe's ONE window. That mode has no phase
	// structure, but it uses the same sampler, so its samples still carry a
	// label rather than a special case in the reduction.
	phaseProbe poolPhase = "probe"
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
