package chronon

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Render invokes the CLI render subcommand with the plan file, assets root and
// output path. It streams output lines with timestamps, tracks progress, and
// runs a stall watchdog to abort hung render processes.
func (c *Client) Render(ctx context.Context, req RenderRequest) error {
	stallTimeout := DefaultStallTimeout
	if env := os.Getenv("CHRONON_STALL_TIMEOUT"); env != "" {
		if d, err := time.ParseDuration(env); err == nil && d > 0 {
			stallTimeout = d
		}
	}

	renderCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := renderArgs(req)
	cmd := exec.CommandContext(renderCtx, c.Binary(), args...)
	cmd.Dir = filepath.Dir(req.PlanPath)
	if req.ReceiptVerify != "" {
		// Canonical verification boundary: RenderingGen requests the policy,
		// Chronon verifies. Passing the resolved policy explicitly (rather
		// than inheriting whatever CHRONON_RECEIPT_VERIFY the ambient
		// environment holds) makes a worker/CLI policy split impossible. The
		// IPC daemon path is unaffected: the daemon reads the same policy
		// from the worker env at startup, and the production hot path is CLI
		// mode.
		cmd.Env = append(os.Environ(), EnvReceiptVerify+"="+req.ReceiptVerify)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("chronon stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("chronon stderr pipe: %w", err)
	}

	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())

	var streamFailed atomic.Bool
	streamLines := func(r io.Reader, prefix string) {
		if err := scanRenderOutput(r, func(line string) {
			lastActivity.Store(time.Now().UnixNano())
			log.Printf("[chronon %s] %s", prefix, line)
			if req.Progress != nil {
				if progress, ok := parseProgressLine(line, req.TotalFrames); ok {
					req.Progress(progress)
				}
			}
		}); err != nil {
			// An output-stream error (a single line beyond the cap, or a pipe
			// failure) must abort loudly. Silently stopping the scanner would
			// freeze progress and the stall heartbeat and end in a misleading
			// watchdog kill of a healthy render.
			streamFailed.Store(true)
			log.Printf("[chronon WARN] output stream %s failed: %v; aborting render", prefix, err)
			cancel()
		}
	}

	renderStart := time.Now()
	log.Printf("[chronon] launching render: %s %s", c.Binary(), strings.Join(args, " "))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("chronon start: %w", err)
	}

	go streamLines(stdoutPipe, "stdout")
	go streamLines(stderrPipe, "stderr")

	// Stall watchdog goroutine
	watchdogDone := make(chan struct{})
	defer close(watchdogDone)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogDone:
				return
			case <-renderCtx.Done():
				return
			case <-ticker.C:
				last := time.Unix(0, lastActivity.Load())
				if time.Since(last) > stallTimeout {
					log.Printf("[chronon WARN] stall detected: no output for %v; aborting render", time.Since(last).Round(time.Second))
					cancel()
					return
				}
			}
		}
	}()

	err = cmd.Wait()
	duration := time.Since(renderStart)
	if err != nil {
		if renderCtx.Err() == context.Canceled && ctx.Err() == nil {
			if streamFailed.Load() {
				return fmt.Errorf("chronon render aborted: output stream failure (aborted after %v): %w", duration, err)
			}
			return fmt.Errorf("chronon render stalled: no output for %v (aborted after %v)", stallTimeout, duration)
		}
		return fmt.Errorf("chronon execution failed after %v: %w", duration, err)
	}
	log.Printf("[chronon] render finished successfully in %v", duration.Round(time.Millisecond))
	return nil
}

// Output streaming (scanRenderOutput), progress-line parsing and the CLI
// renderArgs translation live in output_scan.go and render_args.go.
