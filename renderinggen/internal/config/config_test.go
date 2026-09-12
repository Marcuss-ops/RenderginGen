package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFullConfig(t *testing.T) {
	path := writeConfig(t, `
worker:
  id: renderinggen-77
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
  local_cache_dir: /tmp/cache
chronon:
  backend: software
  home: /opt/chronon3d
  binary: /usr/local/bin/chronon3d_cli
  mode: ipc
  socket_path: /tmp/chronon.sock
gpu:
  device: 0
health:
  addr: ":9090"
workspace:
  root: /tmp/jobs
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Worker.ID != "renderinggen-77" {
		t.Fatalf("worker id = %q", cfg.Worker.ID)
	}
	if cfg.Queue.Endpoint != "http://queue:8081" {
		t.Fatalf("queue endpoint = %q", cfg.Queue.Endpoint)
	}
	if cfg.ArtifactStore.Endpoint != "http://store:9000" {
		t.Fatalf("store endpoint = %q", cfg.ArtifactStore.Endpoint)
	}
	if cfg.Chronon.Backend != "software" {
		t.Fatalf("chronon backend = %q", cfg.Chronon.Backend)
	}
	if cfg.Chronon.Home != "/opt/chronon3d" {
		t.Fatalf("chronon home = %q", cfg.Chronon.Home)
	}
	if cfg.Chronon.Binary != "/usr/local/bin/chronon3d_cli" {
		t.Fatalf("chronon binary = %q", cfg.Chronon.Binary)
	}
	if cfg.Chronon.Mode != "ipc" {
		t.Fatalf("chronon mode = %q", cfg.Chronon.Mode)
	}
	if cfg.Chronon.SocketPath != "/tmp/chronon.sock" {
		t.Fatalf("chronon socket path = %q", cfg.Chronon.SocketPath)
	}
	if cfg.Health.Addr != ":9090" {
		t.Fatalf("health addr = %q", cfg.Health.Addr)
	}
	if cfg.GPU.Device != 0 {
		t.Fatalf("gpu device = %d", cfg.GPU.Device)
	}
	if cfg.Workspace.Root != "/tmp/jobs" {
		t.Fatalf("workspace root = %q", cfg.Workspace.Root)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Worker.ID == "" {
		t.Fatal("worker id should default to a hostname-based value")
	}
	if cfg.Chronon.Backend != "software" {
		t.Fatalf("chronon backend default = %q", cfg.Chronon.Backend)
	}
	if cfg.Chronon.Home != "/opt/chronon3d" {
		t.Fatalf("chronon home default = %q", cfg.Chronon.Home)
	}
	if cfg.Chronon.Mode != "cli" {
		t.Fatalf("chronon mode default = %q", cfg.Chronon.Mode)
	}
	if cfg.Chronon.SocketPath != "/var/run/chronon3d/chronon.sock" {
		t.Fatalf("chronon socket path default = %q", cfg.Chronon.SocketPath)
	}
	if cfg.ArtifactStore.LocalCacheDir != "/var/lib/renderinggen/cache" {
		t.Fatalf("cache dir default = %q", cfg.ArtifactStore.LocalCacheDir)
	}
	if cfg.Health.Addr != ":8080" {
		t.Fatalf("health addr default = %q", cfg.Health.Addr)
	}
	if cfg.Worker.PipelineWorkers != 3 {
		t.Fatalf("pipeline workers default = %d", cfg.Worker.PipelineWorkers)
	}
	if cfg.Worker.GPULanes != 2 {
		t.Fatalf("gpu lanes default = %d", cfg.Worker.GPULanes)
	}
	if cfg.Workspace.Root != defaultWorkspaceRoot() {
		t.Fatalf("workspace root default = %q, want %q", cfg.Workspace.Root, defaultWorkspaceRoot())
	}
}

func TestLoadGPUVulkanNativeProfile(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
chronon:
  profile: gpu-vulkan-native
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Chronon.Backend != "vulkan" || cfg.Chronon.HardwareEncoder != "nvenc" ||
		!cfg.Chronon.StrictNativeBackend || !cfg.Chronon.NativeOutputProfiles || !cfg.Chronon.Report {
		t.Fatalf("profile not applied: %+v", cfg.Chronon)
	}
}

func TestGPUVulkanNativeProfilePreservesExplicitCLITransport(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
chronon:
  profile: gpu-vulkan-native
  mode: cli
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Chronon.Mode != "cli" {
		t.Fatalf("gpu profile changed explicit transport to %q", cfg.Chronon.Mode)
	}
	if !cfg.Chronon.StrictNativeBackend {
		t.Fatal("gpu profile must enable strict native backend")
	}
}

// TestStrictNativeIsDerivedOnceFromProfile pins the single derivation of the
// "this worker demands the native GPU hot path" rule. The wiring used to
// re-compare the raw profile string with `|| cfg.Chronon.Profile ==
// "gpu-vulkan-native"` in three places (twice for the processor, once for the
// Chronon client), which is exactly the shape that drifts when a third profile
// is added.
func TestStrictNativeIsDerivedOnceFromProfile(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		want     bool
		whyCheck string
	}{
		{
			name: "gpu profile",
			yaml: `
queue:
  endpoint: http://q
artifact_store:
  endpoint: http://s
chronon:
  profile: gpu-vulkan-native
`,
			want: true,
		},
		{
			name: "software profile",
			yaml: `
queue:
  endpoint: http://q
artifact_store:
  endpoint: http://s
chronon:
  profile: software-cli
`,
			want: false,
		},
		{
			name: "no profile",
			yaml: `
queue:
  endpoint: http://q
artifact_store:
  endpoint: http://s
`,
			want: false,
		},
		{
			name: "explicit strict flag without profile",
			yaml: `
queue:
  endpoint: http://q
artifact_store:
  endpoint: http://s
chronon:
  strict_native_backend: true
`,
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.yaml))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := cfg.Chronon.StrictNative(); got != tc.want {
				t.Fatalf("StrictNative() = %v, want %v (chronon=%+v)", got, tc.want, cfg.Chronon)
			}
			// The plain field must agree with the derived predicate, otherwise
			// the wiring and the configuration would hold two answers.
			if got, want := cfg.Chronon.StrictNative(), cfg.Chronon.StrictNativeBackend || cfg.Chronon.Profile == ProfileGPUVulkanNative; got != want {
				t.Fatalf("StrictNative() = %v, want %v", got, want)
			}
		})
	}
}

// TestHardwareEncoderDefaultIsTheChrononContract pins the encoder default to
// the single constant the render-args boundary forwards. The value used to be
// a literal in this package and another literal in chronon/render_args.go.
func TestHardwareEncoderDefaultIsTheChrononContract(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
queue:
  endpoint: http://q
artifact_store:
  endpoint: http://s
chronon:
  profile: gpu-vulkan-native
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Chronon.HardwareEncoder != chronon.DefaultHardwareEncoder {
		t.Fatalf("profile default encoder = %q, want the chronon contract value %q", cfg.Chronon.HardwareEncoder, chronon.DefaultHardwareEncoder)
	}
	if cfg.Chronon.PipePixFmt != "nv12" {
		t.Fatalf("profile default pipe pixel format = %q, want nv12", cfg.Chronon.PipePixFmt)
	}
	if chronon.StrictNativeRequired("vulkan", chronon.HardwareEncoderNone) {
		t.Fatal("`none` must disable the native hot path requirement")
	}
	if !chronon.StrictNativeRequired("vulkan", chronon.DefaultHardwareEncoder) {
		t.Fatalf("vulkan + %s must require the native hot path", chronon.DefaultHardwareEncoder)
	}
}

func TestLoadAcceptsPipePixelFormats(t *testing.T) {
	base := `
queue:
  endpoint: http://q
artifact_store:
  endpoint: http://s
chronon:
  backend: software
  pipe_pixfmt: "%s"
`
	for _, format := range []string{"nv12", "p010"} {
		t.Run(format, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, fmt.Sprintf(base, format)))
			if err != nil {
				t.Fatalf("load %s: %v", format, err)
			}
			if cfg.Chronon.PipePixFmt != format {
				t.Fatalf("pipe_pixfmt = %q, want %q", cfg.Chronon.PipePixFmt, format)
			}
		})
	}
	if _, err := Load(writeConfig(t, fmt.Sprintf(base, "rgba"))); err == nil {
		t.Fatal("rgba must be rejected by the worker config contract")
	}
}

func TestLoadArtifactDBConfig(t *testing.T) {
	path := writeConfig(t, `
worker:
  id: renderinggen-77
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
artifact_db:
  path: /var/lib/renderinggen/artifacts.db
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ArtifactDB.Path != "/var/lib/renderinggen/artifacts.db" {
		t.Fatalf("artifact_db.path = %q", cfg.ArtifactDB.Path)
	}
}

func TestLoadArtifactDBDisabledByDefault(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ArtifactDB.Path != "" {
		t.Fatalf("artifact_db.path default = %q, want empty (ledger disabled)", cfg.ArtifactDB.Path)
	}
}

func TestLoadAcceptsValidEncodePreset(t *testing.T) {
	for _, preset := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"} {
		path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
chronon:
  encode_preset: `+preset+`
`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("encode_preset=%q: load: %v", preset, err)
		}
		if cfg.Chronon.EncodePreset != preset {
			t.Fatalf("encode_preset = %q, want %q", cfg.Chronon.EncodePreset, preset)
		}
	}
}

func TestLoadRejectsInvalidEncodePreset(t *testing.T) {
	for _, preset := range []string{"banana", "fast", "slow", "hq", "p0", "p8"} {
		path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
chronon:
  encode_preset: `+preset+`
`)
		if _, err := Load(path); err == nil {
			t.Fatalf("encode_preset=%q: expected validation error, got nil", preset)
		}
	}
}

func TestLoadMissingQueueEndpoint(t *testing.T) {
	path := writeConfig(t, `
artifact_store:
  endpoint: http://store:9000
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing queue.endpoint")
	}
}

func TestLoadMissingStoreEndpoint(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing artifact_store.endpoint")
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
chronon:
  hardware_encder: nvenc
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown/misspelled config key")
	}
}

func TestLoadRejectsUnknownTopLevelKey(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
gpu_lanes: 8
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown top-level key")
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeConfig(t, "[1, 2, 3")
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

// TestLoadRejectsUnforwardableHardwareEncoder pins the config boundary: the
// worker can only forward nvenc / none / empty as --hardware, so any other
// value must fail at load instead of being silently swapped for the native
// default on the first GPU job.
func TestLoadRejectsUnforwardableHardwareEncoder(t *testing.T) {
	base := `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
chronon:
  backend: software
  hardware_encoder: "%s"
`
	cases := []struct {
		encoder string
		wantErr bool
	}{
		{encoder: "", wantErr: false},
		{encoder: "nvenc", wantErr: false},
		{encoder: "none", wantErr: false},
		{encoder: "vaapi", wantErr: true},
		{encoder: "qsv", wantErr: true},
		{encoder: "nvenc ", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.encoder, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, fmt.Sprintf(base, tc.encoder)))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("hardware_encoder %q accepted (cfg=%+v), want a load error", tc.encoder, cfg.Chronon.HardwareEncoder)
				}
				return
			}
			if err != nil {
				t.Fatalf("hardware_encoder %q rejected: %v", tc.encoder, err)
			}
		})
	}
}
