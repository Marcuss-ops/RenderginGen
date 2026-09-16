// daemon_pool.go owns the warm-daemon pool: it starts N Chronon daemons (one
// render socket each), spreads render jobs across them round-robin, waits for
// each one to actually SERVE, and shuts down only the daemons this process
// started.
//
// Why it is here rather than in the one command that needed it. The pool was
// written inline in cmd/batch-render-presets: an unexported handle type, a
// round-robin renderer, a socket-existence wait and a shutdown sequence the
// command had to remember to call on every exit path. That is production
// behaviour (process lifecycle, GPU device assignment, readiness) living in a
// tool with no tests, where a second caller would have had to copy it. It is a
// transport concern — the same reason the IPC client lives in this package.
//
// Readiness is an IPC answer, not a file. The original wait loop polled
// os.Stat(socket) with a fixed 100 ms sleep. A socket FILE appearing proves only
// that the process created the listener, not that the daemon accepts commands: a
// daemon that died between bind and serve, or that is still initializing its
// device, left the caller handing it work that failed. The wait below requires a
// successful STATUS exchange, and it reacts to cancellation, timeout and child
// exit immediately instead of sleeping out a fixed interval.
//
// Child reaping has exactly one owner. cmd.Wait must be called once per process,
// so the goroutine started at spawn owns it for the process's whole life, STORES
// the result and closes a signal channel; nothing else — not the readiness wait,
// not Shutdown — calls Wait again.
//
// Storing the result instead of sending it on a channel is not a detail. The
// original `chan error` was consumed by whichever receiver got there first, and
// the readiness wait is always that receiver when a daemon dies during startup:
// the failure path then blocked on a second receive that nothing would ever
// satisfy, so a daemon that refused to start (a bad asset root, a missing device)
// hung the whole pool instead of reporting why. A closed channel supports any
// number of waiters, and the outcome is read from the handle.
package chronon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// DaemonStartupTimeout is how long one daemon may take to answer STATUS before
// the start fails.
const DaemonStartupTimeout = 60 * time.Second

// daemonProbeInterval is the readiness poll period. Polling is unavoidable
// without a filesystem-watch dependency; what matters is that each poll is a
// real liveness question and that the loop reacts to cancellation and child exit
// immediately rather than after a fixed sleep.
const daemonProbeInterval = 50 * time.Millisecond

// DaemonOptions describes a daemon pool to start.
type DaemonOptions struct {
	// SocketBase is the socket path prefix: daemon i serves "<base>.<i>".
	SocketBase string
	// Count is how many daemons to run. Values below 1 mean one.
	Count int
	// LanesPerDaemon caps concurrent RENDER_JOBs per daemon.
	LanesPerDaemon int
	// Binary is the chronon3d_cli executable.
	Binary string
	// AssetsRoot is the daemon's asset root (-a).
	AssetsRoot string
	// Backend is the render backend (--backend).
	Backend string
	// GPUDevice is the Vulkan device index; it is omitted for the software
	// backend, which has no device to select.
	GPUDevice uint
	// StartupTimeout overrides DaemonStartupTimeout. Zero uses the constant.
	StartupTimeout time.Duration
	// Stdout and Stderr receive the children's output. Nil discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// daemonHandle is one daemon serving one socket.
//
// cmd is nil when the socket was already served: that daemon belongs to someone
// else (an operator's unit, a previous run), so this pool renders through it but
// must never stop it. For an owned daemon, exit carries the single cmd.Wait
// result.
type daemonHandle struct {
	socketPath string
	cmd        *exec.Cmd
	exit       *daemonExit
}

// daemonExit is the one-shot outcome of an owned child process.
//
// exited is CLOSED (never written) once cmd.Wait returned, so every waiter — the
// readiness probe, the failure path, the reaper — observes the same result
// without competing for it, and a waiter that arrives second cannot block.
type daemonExit struct {
	exited chan struct{}
	mu     sync.Mutex
	err    error
}

// watchDaemon starts the single cmd.Wait owner.
func watchDaemon(cmd *exec.Cmd) *daemonExit {
	exit := &daemonExit{exited: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		exit.mu.Lock()
		exit.err = err
		exit.mu.Unlock()
		close(exit.exited)
	}()
	return exit
}

// done returns the channel that closes when the child is reaped.
func (e *daemonExit) done() <-chan struct{} {
	return e.exited
}

// wait blocks until the child is reaped and returns cmd.Wait's result. It is
// idempotent: a closed channel never blocks, so the failure path and the reaper
// can both call it.
func (e *daemonExit) wait() error {
	<-e.exited
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

// DaemonPool spreads render jobs across warm daemons and owns the ones it
// started. It implements Renderer.
type DaemonPool struct {
	handles []daemonHandle
	lanes   []Renderer
	next    atomic.Uint64

	shutdownOnce sync.Once
}

// Compile-time check that DaemonPool satisfies Renderer.
var _ Renderer = (*DaemonPool)(nil)

// StartDaemonPool ensures Count daemons serve SocketBase.0..Count-1.
//
// A socket that is already served is reused and left UNOWNED; the rest are
// started here and waited for, so the returned pool is immediately usable. On
// failure, every daemon this call started is killed before returning, so a
// failed start cannot leave GPU-holding processes behind.
func StartDaemonPool(ctx context.Context, opts DaemonOptions) (*DaemonPool, error) {
	if opts.SocketBase == "" {
		return nil, fmt.Errorf("chronon daemon pool: socket base is required")
	}
	if opts.Binary == "" {
		return nil, fmt.Errorf("chronon daemon pool: binary is required")
	}
	count := opts.Count
	if count < 1 {
		count = 1
	}
	if err := os.MkdirAll(filepath.Dir(opts.SocketBase), 0o755); err != nil {
		return nil, fmt.Errorf("chronon daemon pool: create socket directory: %w", err)
	}

	pool := &DaemonPool{handles: make([]daemonHandle, 0, count), lanes: make([]Renderer, 0, count)}
	for i := 0; i < count; i++ {
		socketPath := fmt.Sprintf("%s.%d", opts.SocketBase, i)
		if _, err := os.Stat(socketPath); err == nil {
			pool.handles = append(pool.handles, daemonHandle{socketPath: socketPath})
			pool.lanes = append(pool.lanes, LimitConcurrency(NewIPCClient(socketPath), opts.LanesPerDaemon))
			continue
		}
		handle, err := startDaemon(ctx, socketPath, opts)
		if err != nil {
			pool.killStarted()
			return nil, err
		}
		pool.handles = append(pool.handles, handle)
		pool.lanes = append(pool.lanes, LimitConcurrency(NewIPCClient(socketPath), opts.LanesPerDaemon))
	}
	return pool, nil
}

// Render spreads jobs round-robin. A lane that is mid-render only queues the
// next job assigned to it instead of stalling the whole pool.
func (p *DaemonPool) Render(ctx context.Context, req RenderRequest) error {
	if len(p.lanes) == 0 {
		return fmt.Errorf("chronon daemon pool has no lanes")
	}
	index := p.next.Add(1) - 1
	return p.lanes[index%uint64(len(p.lanes))].Render(ctx, req)
}

// Sockets returns the sockets the pool renders through, in order.
func (p *DaemonPool) Sockets() []string {
	out := make([]string, 0, len(p.handles))
	for _, handle := range p.handles {
		out = append(out, handle.socketPath)
	}
	return out
}

// OwnedCount reports how many daemons this pool started (as opposed to reused),
// which is how many Shutdown will stop.
func (p *DaemonPool) OwnedCount() int {
	owned := 0
	for _, handle := range p.handles {
		if handle.cmd != nil {
			owned++
		}
	}
	return owned
}

// Shutdown asks every daemon this pool started to stop, then reaps it. Daemons
// that were already serving their socket are left alone. It is idempotent: the
// failure path and the normal exit both call it.
//
// ctx only bounds the graceful stop request; reaping a killed child is not
// cancellable and must not be skipped, or the pool would leak a zombie.
func (p *DaemonPool) Shutdown(ctx context.Context) {
	p.shutdownOnce.Do(func() {
		for _, handle := range p.handles {
			if handle.cmd == nil {
				continue
			}
			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := NewIPCClient(handle.socketPath).Shutdown(shutdownCtx); err != nil {
				// A daemon that will not answer is killed: a process left
				// holding the GPU device would block the next pool.
				_ = handle.cmd.Process.Kill()
			}
			cancel()
			p.reap(handle)
		}
	})
}

// reap waits for the child's single Wait result, escalating to a kill if the
// process is still alive after a grace period.
func (p *DaemonPool) reap(handle daemonHandle) {
	if handle.exit == nil {
		return
	}
	select {
	case <-handle.exit.done():
		return
	case <-time.After(10 * time.Second):
	}
	if handle.cmd.Process != nil {
		_ = handle.cmd.Process.Kill()
	}
	handle.exit.wait()
}

// killStarted kills the daemons this pool started and reaps them; used only on
// the start-failure path, where the daemons may not be serving yet and must not
// be left behind.
func (p *DaemonPool) killStarted() {
	for _, handle := range p.handles {
		if handle.cmd == nil {
			continue
		}
		if handle.cmd.Process != nil {
			_ = handle.cmd.Process.Kill()
		}
		if handle.exit != nil {
			handle.exit.wait()
		}
	}
}

// startDaemon launches one daemon and waits until it answers STATUS.
func startDaemon(ctx context.Context, socketPath string, opts DaemonOptions) (daemonHandle, error) {
	args := []string{"daemon", "-s", socketPath, "-a", opts.AssetsRoot, "--backend", opts.Backend}
	if opts.Backend != "" && opts.Backend != "software" {
		args = append(args, "--gpu-device", strconv.FormatUint(uint64(opts.GPUDevice), 10))
	}
	// The children are bound to the caller's context, so a canceled startup
	// cannot leave a daemon running behind a failed pool.
	cmd := exec.CommandContext(ctx, opts.Binary, args...)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		return daemonHandle{}, fmt.Errorf("chronon daemon pool: start daemon on %s: %w", socketPath, err)
	}
	// One owner for cmd.Wait, for the child's whole life.
	exit := watchDaemon(cmd)

	timeout := opts.StartupTimeout
	if timeout <= 0 {
		timeout = DaemonStartupTimeout
	}
	if err := waitForDaemon(ctx, socketPath, exit, timeout); err != nil {
		_ = cmd.Process.Kill()
		// wait() is idempotent, so this cannot deadlock when the readiness wait
		// already observed the exit — the bug this handle was introduced for.
		exit.wait()
		return daemonHandle{}, err
	}
	return daemonHandle{socketPath: socketPath, cmd: cmd, exit: exit}, nil
}

// waitForDaemon blocks until the daemon at socketPath answers a STATUS command,
// the child exits, the context is canceled, or the timeout expires.
func waitForDaemon(ctx context.Context, socketPath string, exit *daemonExit, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(daemonProbeInterval)
	defer ticker.Stop()

	var lastProbeErr error
	for {
		select {
		case <-exit.done():
			// The daemon is gone; no amount of waiting produces a socket, and
			// exiting immediately beats sleeping out the whole budget for a
			// process that cannot possibly become ready.
			return fmt.Errorf("chronon daemon pool: daemon on %s exited before it was serving (%v)", socketPath, exit.wait())
		case <-ctx.Done():
			return fmt.Errorf("chronon daemon pool: canceled while starting %s: %w", socketPath, ctx.Err())
		case <-ticker.C:
			if time.Now().After(deadline) {
				if lastProbeErr != nil {
					return fmt.Errorf("chronon daemon pool: daemon on %s was not serving within %v (last probe: %w)", socketPath, timeout, lastProbeErr)
				}
				return fmt.Errorf("chronon daemon pool: daemon on %s was not serving within %v", socketPath, timeout)
			}
			if _, err := os.Stat(socketPath); err != nil {
				// Not bound yet: the ordinary case while the engine initializes.
				lastProbeErr = err
				continue
			}
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, probeErr := NewIPCClient(socketPath).Status(probeCtx)
			cancel()
			if probeErr == nil {
				return nil
			}
			// The listener exists but does not answer yet (or at all). Keep
			// probing; the error is reported if the budget runs out.
			if !errors.Is(probeErr, context.DeadlineExceeded) && !errors.Is(probeErr, context.Canceled) {
				lastProbeErr = probeErr
			}
		}
	}
}
