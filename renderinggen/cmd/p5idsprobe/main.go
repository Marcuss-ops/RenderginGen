// Command p5idsprobe replays a captured worker workspace against the Chronon
// daemon three times and prints each artifact size. It is a diagnostic probe,
// so the workspace location is always supplied by the operator.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

func main() {
	source := flag.String("source", os.Getenv("P5_PROBE_SOURCE"), "captured worker workspace containing prepared.json, plan.json and assets/fonts/Poppins-Bold.ttf")
	flag.Parse()
	if *source == "" {
		fmt.Fprintln(os.Stderr, "p5idsprobe: -source or P5_PROBE_SOURCE must point at the captured worker workspace")
		os.Exit(2)
	}
	root := "/tmp/p5-direct-idprobe"
	if err := os.MkdirAll(filepath.Join(root, "assets/fonts"), 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "output"), 0o755); err != nil {
		fatal(err)
	}
	for _, name := range []string{"prepared.json"} {
		b, err := os.ReadFile(filepath.Join(*source, name))
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil {
			fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(*source, "assets/fonts/Poppins-Bold.ttf"))
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets/fonts/Poppins-Bold.ttf"), b, 0o644); err != nil {
		fatal(err)
	}
	for i := 0; i < 3; i++ {
		plan, err := os.ReadFile(filepath.Join(*source, "plan.json"))
		if err != nil {
			fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(plan, &value); err != nil {
			fatal(err)
		}
		value["job_id"] = "p5-direct-unique-job-20261121-" + string(rune('0'+i))
		plan, err = json.Marshal(value)
		if err != nil {
			fatal(err)
		}
		planPath := filepath.Join(root, "plan.json")
		if err := os.WriteFile(planPath, plan, 0o644); err != nil {
			fatal(err)
		}
		output := filepath.Join(root, "output", "result-"+string(rune('0'+i))+".mp4")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err = chronon.NewIPCClient("/var/run/chronon3d/chronon.sock").Render(ctx, chronon.RenderRequest{
			PlanPath: planPath, PreparedPackagePath: filepath.Join(root, "prepared.json"), AssetsRoot: root,
			OutputPath: output, Report: true, ReceiptVerify: "sample", HardwareEncoder: "nvenc", EncoderBackend: "native", TotalFrames: 24,
			Requirements: chronon.ExecutionRequirements{Backend: "vulkan", GPURequired: true, CPUFallbackAllowed: false, CompositionRequired: true, PacketCopyAllowed: true},
			Output:       chronon.OutputSpec{Codec: "h264", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, PipePixFmt: "nv12", FragmentedMP4: true},
		})
		cancel()
		if err != nil {
			fatal(err)
		}
		st, err := os.Stat(output)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s %d\n", output, st.Size())
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "p5idsprobe:", err)
	os.Exit(1)
}
