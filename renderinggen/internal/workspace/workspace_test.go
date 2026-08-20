package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestMaterializeWritesLogicalPaths(t *testing.T) {
	w := newWorkspace(t)

	resolve := func(_ context.Context, asset queue.AssetRef) ([]byte, error) {
		return []byte("bytes:" + asset.Hash), nil
	}

	assets := []queue.AssetRef{
		{Hash: "h-video", LogicalPath: "videos/base.mp4"},
		{Hash: "h-image", LogicalPath: "images/apple.png"},
		{Hash: "h-font", LogicalPath: "fonts/Inter-Bold.ttf"},
	}
	if err := w.Materialize(context.Background(), resolve, assets); err != nil {
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

	resolve := func(_ context.Context, asset queue.AssetRef) ([]byte, error) {
		return nil, errors.New("boom")
	}

	err := w.Materialize(context.Background(), resolve, []queue.AssetRef{
		{Hash: "h", LogicalPath: "videos/base.mp4"},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want resolve error, got %v", err)
	}
}

func TestMaterializeRejectsTraversal(t *testing.T) {
	w := newWorkspace(t)

	resolve := func(_ context.Context, asset queue.AssetRef) ([]byte, error) {
		return []byte("x"), nil
	}

	cases := []string{
		"../escape",
		"a/../../escape",
		"/absolute/path",
	}
	for _, logical := range cases {
		err := w.Materialize(context.Background(), resolve, []queue.AssetRef{
			{Hash: "h", LogicalPath: logical},
		})
		if err == nil {
			t.Fatalf("expected traversal rejection for %q", logical)
		}
	}
}

func TestMaterializeRequiresLogicalPath(t *testing.T) {
	w := newWorkspace(t)

	resolve := func(_ context.Context, asset queue.AssetRef) ([]byte, error) {
		return []byte("x"), nil
	}

	err := w.Materialize(context.Background(), resolve, []queue.AssetRef{{Hash: "h"}})
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
