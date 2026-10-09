package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

func TestNewRejectsConcurrentSameJobWorkspace(t *testing.T) {
	root := t.TempDir()
	first, err := New(root, "same-job")
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	defer first.Cleanup()
	if _, err := New(root, "same-job"); !errors.Is(err, errWorkspaceBusy) {
		t.Fatalf("second New error = %v, want already-owned error", err)
	}
}

func TestConcurrentDifferentJobWorkspacesRemainIsolated(t *testing.T) {
	root := t.TempDir()
	const workers = 12
	workspaces := make([]*Workspace, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, err := New(root, fmt.Sprintf("job-%02d", i))
			if err != nil {
				t.Errorf("New(%d): %v", i, err)
				return
			}
			workspaces[i] = w
		}(i)
	}
	wg.Wait()
	for i, w := range workspaces {
		if w == nil {
			continue
		}
		if err := w.WritePlan([]byte(fmt.Sprintf("job=%d", i))); err != nil {
			t.Errorf("WritePlan(%d): %v", i, err)
		}
	}
	for _, w := range workspaces {
		if w != nil {
			if err := w.Cleanup(); err != nil {
				t.Errorf("Cleanup: %v", err)
			}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".workspace-lock-") && entry.Name() != ".workspace-lock-registry" {
			t.Errorf("inactive per-job lock file leaked: %s", entry.Name())
		}
	}
}

func TestWorkspaceLockIsCrossProcess(t *testing.T) {
	if os.Getenv("RENDERINGGEN_WORKSPACE_LOCK_HELPER") == "1" {
		root, jobID := os.Getenv("RENDERINGGEN_WORKSPACE_LOCK_ROOT"), os.Getenv("RENDERINGGEN_WORKSPACE_LOCK_JOB")
		w, err := New(root, jobID)
		if err == nil {
			_ = w.Cleanup()
			os.Exit(2)
		}
		if !errors.Is(err, errWorkspaceBusy) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	w, err := New(root, "cross-process")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cleanup()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkspaceLockIsCrossProcess$")
	cmd.Env = append(os.Environ(), "RENDERINGGEN_WORKSPACE_LOCK_HELPER=1", "RENDERINGGEN_WORKSPACE_LOCK_ROOT="+root, "RENDERINGGEN_WORKSPACE_LOCK_JOB=cross-process")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper should observe active process lock, err=%v output=%s", err, output)
	}
}

func TestCleanupStaleRespectsProcessLockAndExpiresAfterOwnerEnds(t *testing.T) {
	root := t.TempDir()
	w, err := New(root, "running")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(w.Root(), past, past); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStale(root, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Root()); err != nil {
		t.Fatalf("live locked workspace removed: %v", err)
	}
	if err := w.Cleanup(); err != nil {
		t.Fatal(err)
	}
	staleDir := filepath.Join(root, "stale")
	if err := os.MkdirAll(filepath.Join(staleDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staleDir, "output"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staleDir, past, past); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStale(root, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("unowned stale directory remains: %v", err)
	}
}

func newWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w, err := New(t.TempDir(), "job-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.Cleanup() })
	return w
}

func TestNewCreatesDirs(t *testing.T) {
	w := newWorkspace(t)

	for _, dir := range []string{w.Root(), w.assetsRoot(), filepath.Dir(w.OutputPath("result.mp4"))} {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Fatalf("expected dir %s, err=%v", dir, err)
		}
	}
	if w.PlanPath() != filepath.Join(w.Root(), "plan.json") {
		t.Fatalf("plan path = %q", w.PlanPath())
	}
	if w.PreparedPackagePath() != filepath.Join(w.Root(), "prepared.json") {
		t.Fatalf("prepared package path = %q", w.PreparedPackagePath())
	}
}

func TestNewRequiresJobID(t *testing.T) {
	for _, id := range []string{"", "../escape", "a/../../escape", "/absolute", "bad\\\\name", "bad name", strings.Repeat("x", 257)} {
		if _, err := New(t.TempDir(), id); err == nil {
			t.Errorf("expected unsafe job id %q to be rejected", id)
		}
	}
}

func TestNewRejectsConcurrentWorkspaceOwner(t *testing.T) {
	root := t.TempDir()
	owner, err := New(root, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Cleanup()
	if _, err := New(root, "job-1"); !errors.Is(err, errWorkspaceBusy) {
		t.Fatalf("second workspace owner error = %v, want errWorkspaceBusy", err)
	}
}

func TestNewRejectsSymlinkedIDComponent(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, "link/job-1"); err == nil {
		t.Fatal("workspace creation followed a symlink outside jobsRoot")
	}
	if _, err := os.Stat(filepath.Join(outside, "job-1")); !os.IsNotExist(err) {
		t.Fatalf("workspace escaped through symlink: stat err=%v", err)
	}
}

func TestMaterializeDoesNotFollowSymlinkedAssetDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	w, err := New(root, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cleanup()
	if err := os.RemoveAll(filepath.Join(w.Root(), "assets")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(w.Root(), "assets")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = w.MaterializePaths(context.Background(), func(context.Context, queue.AssetRef) (ResolvedAsset, error) {
		return ResolvedAsset{LocalPath: source}, nil
	}, []queue.AssetRef{{Hash: "asset", LogicalPath: "assets/escape.bin"}})
	if err == nil {
		t.Fatal("materialization should reject the symlinked in-root component")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.bin")); !os.IsNotExist(err) {
		t.Fatalf("materialization escaped workspace: %v", err)
	}
	if err := w.Cleanup(); err != nil {
		t.Fatalf("cleanup must unlink the replaced job entry without following its symlink: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("cleanup changed external directory: %v", err)
	}
}

func TestCleanupStaleUsesPinnedRootAndSkipsReplacedCandidates(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	job := filepath.Join(root, "old-job")
	if err := os.Mkdir(job, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(job, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(job); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, job); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStale(root, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory unexpectedly affected: %v", err)
	}
}

func TestCleanupUsesPinnedJobsRootAfterPathSwap(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	jobsRoot := filepath.Join(parent, "jobs")
	if err := os.Mkdir(jobsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := New(jobsRoot, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "moved-jobs")
	if err := os.Rename(jobsRoot, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, jobsRoot); err != nil {
		t.Fatal(err)
	}
	if err := w.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "job-1")); !os.IsNotExist(err) {
		t.Fatalf("cleanup failed to remove workspace from pinned jobs root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "job-1")); !os.IsNotExist(err) {
		t.Fatalf("cleanup escaped through swapped jobs root: %v", err)
	}
}

func TestWriteLeaseAndOutputUsePinnedWorkspaceRoot(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	w, err := New(parent, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cleanup()
	moved := filepath.Join(parent, "moved-job")
	if err := os.Rename(w.Root(), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, w.Root()); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteLease(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, leaseMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("lease write escaped: %v", err)
	}
	output, err := w.OpenOutput("result.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = output.Write([]byte("ok"))
	_ = output.Close()
	if _, err := os.Stat(filepath.Join(outside, "output", "result.mp4")); !os.IsNotExist(err) {
		t.Fatalf("output write escaped: %v", err)
	}
}

// pathResolverFor writes each asset's fixture bytes into a source directory
// for the workspace's path-based materializer.
func pathResolverFor(t *testing.T, sourceDir string) PathResolver {
	t.Helper()
	return func(_ context.Context, asset queue.AssetRef) (ResolvedAsset, error) {
		p := filepath.Join(sourceDir, asset.Hash)
		data := []byte("bytes:" + asset.Hash)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return ResolvedAsset{}, err
		}
		return ResolvedAsset{LocalPath: p, SizeBytes: int64(len(data))}, nil
	}
}

func TestMaterializeWritesLogicalPaths(t *testing.T) {
	// The source directory and workspace share one temp root to exercise the
	// common local-cache materialization case.
	root := t.TempDir()
	sources := filepath.Join(root, "sources")
	if err := os.MkdirAll(sources, 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := New(root, "job-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.Cleanup() })

	assets := []queue.AssetRef{
		{Hash: "h-video", LogicalPath: "videos/base.mp4"},
		{Hash: "h-image", LogicalPath: "images/apple.png"},
		{Hash: "h-font", LogicalPath: "fonts/Inter-Bold.ttf"},
	}
	if err := w.MaterializePaths(context.Background(), pathResolverFor(t, sources), assets); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	for _, a := range assets {
		p := filepath.Join(w.assetsRoot(), filepath.FromSlash(a.LogicalPath))
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if string(data) != "bytes:"+a.Hash {
			t.Fatalf("%s content = %q", p, data)
		}
	}
}

func TestMaterializeResolveError(t *testing.T) {
	w := newWorkspace(t)

	resolve := func(_ context.Context, _ queue.AssetRef) (ResolvedAsset, error) {
		return ResolvedAsset{}, errors.New("boom")
	}

	err := w.MaterializePaths(context.Background(), resolve, []queue.AssetRef{
		{Hash: "h", LogicalPath: "videos/base.mp4"},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want resolve error, got %v", err)
	}
}

func TestMaterializeRejectsTraversal(t *testing.T) {
	w := newWorkspace(t)

	resolve := func(_ context.Context, _ queue.AssetRef) (ResolvedAsset, error) {
		return ResolvedAsset{LocalPath: "/does/not/matter"}, nil
	}

	cases := []string{
		"../escape",
		"a/../../escape",
		"/absolute/path",
	}
	for _, logical := range cases {
		err := w.MaterializePaths(context.Background(), resolve, []queue.AssetRef{
			{Hash: "h", LogicalPath: logical},
		})
		if err == nil {
			t.Fatalf("expected traversal rejection for %q", logical)
		}
	}
}

func TestMaterializeRequiresLogicalPath(t *testing.T) {
	w := newWorkspace(t)

	resolve := func(_ context.Context, _ queue.AssetRef) (ResolvedAsset, error) {
		return ResolvedAsset{LocalPath: "/does/not/matter"}, nil
	}

	err := w.MaterializePaths(context.Background(), resolve, []queue.AssetRef{{Hash: "h"}})
	if err == nil {
		t.Fatal("expected error for missing logical_path")
	}
}

func TestWritePlan(t *testing.T) {
	w := newWorkspace(t)

	if err := w.WritePlan([]byte(`{"schema":"chronon.render-plan.v2"}`)); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	data, err := os.ReadFile(w.PlanPath())
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if string(data) != `{"schema":"chronon.render-plan.v2"}` {
		t.Fatalf("plan content = %q", data)
	}
}

func TestWritePreparedPackage(t *testing.T) {
	w := newWorkspace(t)
	const payload = `{"content_hash":"abc"}`
	if err := w.WritePreparedPackage([]byte(payload)); err != nil {
		t.Fatalf("write prepared package: %v", err)
	}
	data, err := os.ReadFile(w.PreparedPackagePath())
	if err != nil {
		t.Fatalf("read prepared package: %v", err)
	}
	if string(data) != payload {
		t.Fatalf("prepared package = %q, want %q", data, payload)
	}
}

func TestCleanupStaleRemovesExpiredAndKeepsActiveLease(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	active := filepath.Join(root, "active")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(active, 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{old, active} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(active, ".lease_until"), []byte(time.Now().Add(time.Hour).Format(time.RFC3339Nano)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStale(root, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old workspace remains: %v", err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active workspace removed: %v", err)
	}
}

func TestWriteLeaseProtectsFromCleanupStale(t *testing.T) {
	root := t.TempDir()
	active, err := New(root, "active")
	if err != nil {
		t.Fatalf("New(active): %v", err)
	}
	expired, err := New(root, "expired")
	if err != nil {
		t.Fatalf("New(expired): %v", err)
	}
	if err := active.WriteLease(time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("WriteLease(active): %v", err)
	}
	if err := expired.WriteLease(time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("WriteLease(expired): %v", err)
	}
	// Simulate a long render: the workspace directories have not been touched
	// for hours (a running render writes nothing new to the directory tree;
	// only the lease marker keeps it alive). Set the old mtime AFTER writing
	// the markers, because creating the marker bumps the directory mtime.
	past := time.Now().Add(-2 * time.Hour)
	for _, w := range []*Workspace{active, expired} {
		if err := os.Chtimes(w.Root(), past, past); err != nil {
			t.Fatal(err)
		}
	}
	// The active workspace must hold its process lock so the sweeper does not
	// mistake this same-process test fixture for an abandoned workspace.
	// Release only the expired workspace lock to model a process crash.
	if err := unlockWorkspaceSlot(expired.lockFile); err != nil {
		t.Fatal(err)
	}
	if err := expired.lockFile.Close(); err != nil {
		t.Fatal(err)
	}
	expired.lockFile = nil
	if err := expired.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStale(root, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(active.Root()); err != nil {
		t.Fatalf("workspace with a valid lease marker was removed: %v", err)
	}
	if _, err := os.Stat(expired.Root()); !os.IsNotExist(err) {
		t.Fatalf("workspace with an expired lease marker was kept: %v", err)
	}
}

func TestCleanupRemovesTree(t *testing.T) {
	root := t.TempDir()
	w, err := New(root, "job-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.Cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(w.Root()); !os.IsNotExist(err) {
		t.Fatalf("workspace should be removed, stat err = %v", err)
	}
}
