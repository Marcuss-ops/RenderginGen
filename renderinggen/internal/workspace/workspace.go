// Package workspace prepares and cleans a per-job workspace on the worker:
//
//	/var/lib/renderinggen/jobs/<jobID>/
//	├── plan.json           (render_plan written by the caller)
//	├── assets/<logical>    (assets materialized by content hash)
//	└── output/result.mp4   (render output)
//
// Materialize is the single asset resolver/materializer: it pulls each asset
// through the L1/L2/L3 cache (via a resolver func) and writes it to the
// logical path declared by the job, so the render_plan's source/asset
// references resolve inside the assets root.
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// Resolver returns the bytes for an asset hash (L1 -> L2 -> L3).
type Resolver func(ctx context.Context, hash string) ([]byte, error)

// Workspace is a per-job directory tree prepared for a render.
type Workspace struct {
	root      string
	assetsDir string
	outputDir string
}

// New creates the job workspace directory tree under jobsRoot.
func New(jobsRoot, jobID string) (*Workspace, error) {
	if jobID == "" {
		return nil, fmt.Errorf("workspace: job id is required")
	}
	root := filepath.Join(jobsRoot, jobID)
	w := &Workspace{
		root:      root,
		assetsDir: filepath.Join(root, "assets"),
		outputDir: filepath.Join(root, "output"),
	}
	for _, dir := range []string{w.assetsDir, w.outputDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("workspace: create %s: %w", dir, err)
		}
	}
	return w, nil
}

// Root returns the workspace root directory.
func (w *Workspace) Root() string { return w.root }

// AssetsRoot returns the directory assets are materialized into.
func (w *Workspace) AssetsRoot() string { return w.assetsDir }

// OutputPath returns the path for a rendered output file.
func (w *Workspace) OutputPath(name string) string {
	return filepath.Join(w.outputDir, name)
}

// PlanPath returns the path of the render plan written for Chronon.
func (w *Workspace) PlanPath() string {
	return filepath.Join(w.root, "plan.json")
}

// WritePlan writes the render plan document to plan.json.
func (w *Workspace) WritePlan(plan []byte) error {
	return os.WriteFile(w.PlanPath(), plan, 0o644)
}

// Materialize resolves every asset through the resolver and writes it to its
// logical path under the assets root. Each logical path is validated to stay
// inside the assets root (no traversal).
func (w *Workspace) Materialize(ctx context.Context, resolve Resolver, assets []queue.AssetRef) error {
	for _, a := range assets {
		if err := w.materializeOne(ctx, resolve, a); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workspace) materializeOne(ctx context.Context, resolve Resolver, a queue.AssetRef) error {
	data, err := resolve(ctx, a.Hash)
	if err != nil {
		return fmt.Errorf("workspace: resolve %s: %w", a.Hash, err)
	}
	dst, err := w.assetPath(a.LogicalPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("workspace: write %s: %w", dst, err)
	}
	return nil
}

// assetPath validates a logical path and joins it under the assets root.
func (w *Workspace) assetPath(logical string) (string, error) {
	if logical == "" {
		return "", fmt.Errorf("workspace: logical_path is required")
	}
	clean := filepath.Clean(logical)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace: logical_path %q escapes the assets root", logical)
	}
	return filepath.Join(w.assetsDir, clean), nil
}

// Cleanup removes the workspace directory tree.
func (w *Workspace) Cleanup() error {
	if err := os.RemoveAll(w.root); err != nil {
		return fmt.Errorf("workspace: cleanup %s: %w", w.root, err)
	}
	return nil
}
