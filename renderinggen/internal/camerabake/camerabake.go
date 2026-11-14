// Package camerabake runs the installed CM3D CameraRig binary over a render-plan
// document. It is the infrastructure side of overlay.CameraSampleBaker: the
// compiler owns what a resolved camera sample must satisfy, this package owns
// the process, the temporary file and the operator-facing timeout.
package camerabake

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds one baker invocation. A hung native binary must not hold
// a worker job (and its lease) indefinitely.
const DefaultTimeout = 60 * time.Second

// Exec bakes a camera plan by invoking a native executable on a temporary file.
// An empty Binary fails closed with the configuration name the operator must
// set, instead of silently switching the camera authority to another path.
type Exec struct {
	Binary  string
	Timeout time.Duration
}

// FromEnv resolves the runner from CHRONON_CAMERA_PLAN_BAKER and
// CHRONON_CAMERA_PLAN_BAKER_TIMEOUT. It always returns a runner: the empty
// binary case is reported at Bake time, so a worker starts and fails the job it
// cannot render instead of refusing to boot.
func FromEnv() *Exec {
	return &Exec{
		Binary:  strings.TrimSpace(os.Getenv("CHRONON_CAMERA_PLAN_BAKER")),
		Timeout: timeoutFromEnv(),
	}
}

// New returns a runner for an explicit binary and timeout. A non-positive
// timeout falls back to DefaultTimeout.
func New(binary string, timeout time.Duration) *Exec {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Exec{Binary: strings.TrimSpace(binary), Timeout: timeout}
}

func timeoutFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CHRONON_CAMERA_PLAN_BAKER_TIMEOUT"))
	if raw == "" {
		return DefaultTimeout
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return DefaultTimeout
	}
	return timeout
}

// Bake writes the plan to a temporary file, runs the baker in place and returns
// the rewritten document. The compiler validates the returned samples.
func (e *Exec) Bake(planJSON []byte) ([]byte, error) {
	if e == nil || e.Binary == "" {
		return nil, fmt.Errorf("CM3D CameraRig baker is not configured (set CHRONON_CAMERA_PLAN_BAKER)")
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	file, err := os.CreateTemp("", "renderinggen-camera-plan-*.json")
	if err != nil {
		return nil, fmt.Errorf("create temporary camera plan: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(planJSON); err != nil {
		file.Close()
		return nil, fmt.Errorf("write temporary camera plan: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close temporary camera plan: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.Binary, path)
	// WaitDelay guarantees Wait returns even when a killed baker leaves a child
	// process holding the output pipe open.
	cmd.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("CM3D CameraRig baker did not finish within %s: %w: %s", timeout, err, strings.TrimSpace(output.String()))
		}
		return nil, fmt.Errorf("CM3D CameraRig baker failed: %w: %s", err, strings.TrimSpace(output.String()))
	}
	baked, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baked camera plan: %w", err)
	}
	return baked, nil
}
