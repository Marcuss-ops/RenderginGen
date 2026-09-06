package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

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

	for _, dir := range []string{w.Root(), w.AssetsRoot(), filepath.Dir(w.OutputPath("result.mp4"))} {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Fatalf("expected dir %s, err=%v", dir, err)
		}
	}
	if w.PlanPath() != filepath.Join(w.Root(), "plan.json") {
		t.Fatalf("plan path = %q", w.PlanPath())
	}
}

func TestNewRequiresJobID(t *testing.T) {
	if _, err := New(t.TempDir(), ""); err == nil {
		t.Fatal("expected error for empty job id")
	}
}

// pathResolverFor writes each asset's fixture bytes into a source directory on
// the same filesystem as the workspace so MaterializePaths can hard-link them.
// It is the path-resolver counterpart of the old byte-resolver fixtures: the
// workspace API deliberately never sees []byte, so fixtures stage files.
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
	// The source directory and the workspace share one temp root so the hard
	// link never crosses a filesystem boundary (which would fall back to a
	// streaming copy instead of exercising the link path).
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
		p := filepath.Join(w.AssetsRoot(), filepath.FromSlash(a.LogicalPath))
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

	if err := w.WritePlan([]byte(`{"schema":"chronon.render-plan"}`)); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	data, err := os.ReadFile(w.PlanPath())
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if string(data) != `{"schema":"chronon.render-plan"}` {
		t.Fatalf("plan content = %q", data)
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
