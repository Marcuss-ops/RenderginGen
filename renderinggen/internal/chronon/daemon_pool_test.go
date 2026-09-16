package chronon

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIPC daemon serves the STATUS handshake on a real UNIX socket, so the
// readiness wait is exercised against the actual wire protocol instead of a stub
// of it.
type fakeIPCDaemon struct {
	listener net.Listener
	socket   string
}

func startFakeIPCDaemon(t *testing.T, socketPath string) *fakeIPCDaemon {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen %s: %v", socketPath, err)
	}
	daemon := &fakeIPCDaemon{listener: listener, socket: socketPath}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				header := make([]byte, ipcHeaderBytes)
				if _, err := io.ReadFull(c, header); err != nil {
					return
				}
				if payloadLen := binary.BigEndian.Uint32(header[8:12]); payloadLen > 0 {
					if _, err := io.ReadFull(c, make([]byte, payloadLen)); err != nil {
						return
					}
				}
				reply := make([]byte, ipcHeaderBytes)
				binary.BigEndian.PutUint32(reply[0:4], ipcMagic)
				binary.BigEndian.PutUint32(reply[4:8], ipcStatusOk)
				binary.BigEndian.PutUint32(reply[8:12], 0)
				_, _ = c.Write(reply)
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return daemon
}

// stalledExit is an owned child that is still running: its signal channel is
// open and will never close.
func stalledExit() *daemonExit {
	return &daemonExit{exited: make(chan struct{})}
}

// reapedExit is an owned child that already exited with err.
func reapedExit(err error) *daemonExit {
	exit := &daemonExit{exited: make(chan struct{}), err: err}
	close(exit.exited)
	return exit
}

// TestDaemonExitWaitIsIdempotent is the regression for the pool deadlock: the
// readiness wait and the failure path both observe the same reaped child, and
// the second read must not block. The previous `chan error` was consumed by the
// first reader, so a daemon that died during startup hung StartDaemonPool
// forever instead of reporting the exit.
func TestDaemonExitWaitIsIdempotent(t *testing.T) {
	exit := reapedExit(errors.New("exit status 1"))
	done := make(chan error, 1)
	go func() { done <- exit.wait() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "exit status 1") {
			t.Fatalf("wait returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first wait blocked on a reaped child")
	}
	// The second waiter (the failure path) must observe the same result at once.
	second := make(chan error, 1)
	go func() { second <- exit.wait() }()
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the second wait blocked on a reaped child; the pool deadlocks when a daemon dies at startup")
	}
}

// TestStartDaemonPoolReportsACrashedDaemon proves the deadlock is gone end to
// end: a binary that exits immediately must produce an ERROR quickly, not a hang.
func TestStartDaemonPoolReportsACrashedDaemon(t *testing.T) {
	dir := t.TempDir()
	// /bin/true exits 0 without ever creating the socket the pool waits for, which
	// is exactly what a daemon with a bad asset root or a missing device does.
	start := time.Now()
	_, err := StartDaemonPool(context.Background(), DaemonOptions{
		SocketBase: filepath.Join(dir, "crashed.sock"), Count: 1, Binary: "/bin/true",
		StartupTimeout: 60 * time.Second,
	})
	if err == nil {
		t.Fatal("a daemon that never serves must fail the pool")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the pool took %v to report a daemon that exited at once", elapsed)
	}
	if !strings.Contains(err.Error(), "exited before it was serving") {
		t.Fatalf("error = %v, want an exit-detected failure", err)
	}
}

// TestWaitForDaemonRequiresAnAnsweringSocket is the regression the readiness
// check exists for: a socket FILE that nobody serves must not pass as ready, and
// a listener that answers STATUS must.
func TestWaitForDaemonRequiresAnAnsweringSocket(t *testing.T) {
	dir := t.TempDir()

	// A file that is not a listener: os.Stat succeeds, the STATUS probe cannot.
	dud := filepath.Join(dir, "dud.sock")
	if err := os.WriteFile(dud, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	never := stalledExit()
	err := waitForDaemon(context.Background(), dud, never, 300*time.Millisecond)
	if err == nil {
		t.Fatal("a socket file with no listener must not be reported as serving")
	}
	if !strings.Contains(err.Error(), "was not serving") {
		t.Fatalf("error = %v, want a 'was not serving' timeout failure", err)
	}

	// A real listener that answers the handshake is ready.
	live := filepath.Join(dir, "live.sock")
	startFakeIPCDaemon(t, live)
	if err := waitForDaemon(context.Background(), live, never, 5*time.Second); err != nil {
		t.Fatalf("an answering daemon must be reported as serving: %v", err)
	}
}

// TestWaitForDaemonReactsImmediately pins that the wait does not sleep out its
// whole budget when the outcome is already known: a child that exited and a
// canceled context both return at once.
func TestWaitForDaemonReactsImmediately(t *testing.T) {
	dir := t.TempDir()

	exited := reapedExit(errors.New("exit status 1"))
	start := time.Now()
	err := waitForDaemon(context.Background(), filepath.Join(dir, "gone.sock"), exited, 30*time.Second)
	if err == nil || !strings.Contains(err.Error(), "exited before it was serving") {
		t.Fatalf("error = %v, want an exit-detected failure", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the wait slept %v after the child exited; it must react immediately", elapsed)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	err = waitForDaemon(canceled, filepath.Join(dir, "canceled.sock"), stalledExit(), 30*time.Second)
	if err == nil || !strings.Contains(err.Error(), "canceled while starting") {
		t.Fatalf("error = %v, want a cancellation failure", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the wait slept %v after cancellation; it must react immediately", elapsed)
	}
}

// countingRenderer records how many renders each lane received.
type countingRenderer struct {
	mu    sync.Mutex
	calls int
}

func (c *countingRenderer) Render(context.Context, RenderRequest) error {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return nil
}

// TestDaemonPoolSpreadsJobsRoundRobin pins the routing policy and the ownership
// reporting the shutdown path depends on.
func TestDaemonPoolSpreadsJobsRoundRobin(t *testing.T) {
	first, second := &countingRenderer{}, &countingRenderer{}
	pool := &DaemonPool{
		// Only the socket paths and lanes matter for routing.
		handles: []daemonHandle{{socketPath: "/run/a.0"}, {socketPath: "/run/a.1"}}, // unowned: no cmd, no exit
		lanes:   []Renderer{first, second},
	}
	for i := 0; i < 5; i++ {
		if err := pool.Render(context.Background(), RenderRequest{}); err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
	}
	if first.calls != 3 || second.calls != 2 {
		t.Fatalf("lane usage = %d/%d, want 3/2 for round-robin", first.calls, second.calls)
	}
	if got := pool.Sockets(); len(got) != 2 || got[0] != "/run/a.0" || got[1] != "/run/a.1" {
		t.Fatalf("sockets = %v", got)
	}
	// Both handles are unowned (cmd nil), so nothing is stopped and the count is
	// zero: Shutdown must leave a daemon this pool did not start alone.
	if owned := pool.OwnedCount(); owned != 0 {
		t.Fatalf("owned = %d, want 0 for reused sockets", owned)
	}
	pool.Shutdown(context.Background())
	pool.Shutdown(context.Background()) // idempotent
}

// TestStartDaemonPoolReusesServedSocket pins that a socket already served is
// reused unowned, and that input validation fails rather than starting
// something.
func TestStartDaemonPoolReusesServedSocket(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "chronon.sock")
	if err := os.WriteFile(base+".0", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	pool, err := StartDaemonPool(context.Background(), DaemonOptions{
		SocketBase: base, Count: 1, Binary: "/nonexistent/chronon3d_cli",
	})
	if err != nil {
		t.Fatalf("an already-served socket must be reused without starting anything: %v", err)
	}
	if pool.OwnedCount() != 0 {
		t.Fatalf("owned = %d, want 0", pool.OwnedCount())
	}
	pool.Shutdown(context.Background())

	if _, err := StartDaemonPool(context.Background(), DaemonOptions{}); err == nil {
		t.Error("a missing socket base must be rejected")
	}
	if _, err := StartDaemonPool(context.Background(), DaemonOptions{SocketBase: base}); err == nil {
		t.Error("a missing binary must be rejected")
	}
	// A socket that is not served requires starting the binary; a binary that
	// does not exist must fail loudly and leave nothing behind.
	if _, err := StartDaemonPool(context.Background(), DaemonOptions{
		SocketBase: filepath.Join(dir, "fresh.sock"), Count: 1, Binary: "/nonexistent/chronon3d_cli",
	}); err == nil {
		t.Error("starting a daemon from a missing binary must fail")
	}
}
