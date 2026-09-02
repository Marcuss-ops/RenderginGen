// Package workspace prepares and cleans a per-job workspace on the worker:
//
//	/var/lib/renderinggen/jobs/<jobID>/
//	├── plan.json           (render_plan written by the caller)
//	├── assets/<logical>    (assets materialized by content hash)
//	└── output/result.mp4   (render output)
//
// Materialization is path-first for large media: callers can resolve a local
// CAS path and this package hardlinks it into the workspace when possible,
// falling back to a streaming copy across filesystems. The byte resolver is
// retained for compatibility and small fixtures.
package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// Resolver returns bytes for a compatibility asset path.
type Resolver func(ctx context.Context, asset queue.AssetRef) ([]byte, error)

// ResolvedAsset is the canonical file-backed asset resolution result.
type ResolvedAsset struct {
	LocalPath string
	Size      int64
}

// PathResolver resolves an asset to an already-local content-addressed file.
type PathResolver func(ctx context.Context, asset queue.AssetRef) (ResolvedAsset, error)

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

func (w *Workspace) Root() string       { return w.root }
func (w *Workspace) AssetsRoot() string { return w.assetsDir }
func (w *Workspace) OutputPath(name string) string {
	return filepath.Join(w.outputDir, name)
}
func (w *Workspace) PlanPath() string { return filepath.Join(w.root, "plan.json") }
func (w *Workspace) WritePlan(plan []byte) error {
	return os.WriteFile(w.PlanPath(), plan, 0o644)
}

// Materialize resolves every asset through the compatibility byte resolver.
func (w *Workspace) Materialize(ctx context.Context, resolve Resolver, assets []queue.AssetRef) error {
	return w.runMaterializers(ctx, assets, func(ctx context.Context, a queue.AssetRef) error {
		return w.materializeOne(ctx, resolve, a)
	})
}

// MaterializePaths resolves every asset to a local path and installs it into
// the job workspace without loading the media into the Go heap.
func (w *Workspace) MaterializePaths(ctx context.Context, resolve PathResolver, assets []queue.AssetRef) error {
	return w.runMaterializers(ctx, assets, func(ctx context.Context, a queue.AssetRef) error {
		return w.materializeOnePath(ctx, resolve, a)
	})
}

func (w *Workspace) runMaterializers(ctx context.Context, assets []queue.AssetRef, materialize func(context.Context, queue.AssetRef) error) error {
	if len(assets) == 0 {
		return nil
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan queue.AssetRef)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	workerCount := 4
	if len(assets) < workerCount {
		workerCount = len(assets)
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				if err := materialize(workCtx, a); err != nil {
					errOnce.Do(func() { firstErr = err; cancel() })
				}
			}
		}()
	}

send:
	for _, a := range assets {
		select {
		case jobs <- a:
		case <-workCtx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func (w *Workspace) materializeOne(ctx context.Context, resolve Resolver, a queue.AssetRef) error {
	dst, err := w.assetPath(a.LogicalPath)
	if err != nil {
		return err
	}
	data, err := resolve(ctx, a)
	if err != nil {
		return fmt.Errorf("workspace: resolve %s: %w", a.Hash, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("workspace: write %s: %w", dst, err)
	}
	return nil
}

func (w *Workspace) materializeOnePath(ctx context.Context, resolve PathResolver, a queue.AssetRef) error {
	dst, err := w.assetPath(a.LogicalPath)
	if err != nil {
		return err
	}
	resolved, err := resolve(ctx, a)
	if err != nil {
		return fmt.Errorf("workspace: resolve path %s: %w", a.Hash, err)
	}
	if resolved.LocalPath == "" {
		return fmt.Errorf("workspace: resolved asset %s has empty local path", a.Hash)
	}
	sourceInfo, err := os.Stat(resolved.LocalPath)
	if err != nil {
		return fmt.Errorf("workspace: stat resolved asset %s: %w", a.Hash, err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("workspace: resolved asset %s is not a regular file", a.Hash)
	}
	if resolved.Size >= 0 && resolved.Size != sourceInfo.Size() {
		return fmt.Errorf("workspace: resolved asset %s size %d != expected %d", a.Hash, sourceInfo.Size(), resolved.Size)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if same, err := sameFilePath(resolved.LocalPath, dst); err == nil && same {
		return nil
	}
	_ = os.Remove(dst)
	if err := os.Link(resolved.LocalPath, dst); err == nil {
		return nil
	}

	// Cross-device fallback: stream source -> temp -> atomic rename.
	input, err := os.Open(resolved.LocalPath)
	if err != nil {
		return fmt.Errorf("workspace: open resolved asset %s: %w", a.Hash, err)
	}
	defer input.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".materialize-*")
	if err != nil {
		return fmt.Errorf("workspace: create temp for %s: %w", dst, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, input); err != nil {
		tmp.Close()
		return fmt.Errorf("workspace: copy %s: %w", dst, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("workspace: close temp %s: %w", dst, err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("workspace: install %s: %w", dst, err)
	}
	return nil
}

func sameFilePath(source, dest string) (bool, error) {
	srcInfo, err := os.Stat(source)
	if err != nil {
		return false, err
	}
	dstInfo, err := os.Stat(dest)
	if err != nil {
		return false, err
	}
	return os.SameFile(srcInfo, dstInfo), nil
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
	if clean == "assets" || strings.HasPrefix(clean, "assets"+string(filepath.Separator)) {
		return filepath.Join(w.root, clean), nil
	}
	return filepath.Join(w.assetsDir, clean), nil
}

func CleanupStale(root string, olderThan time.Duration) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-olderThan)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		leasePath := filepath.Join(path, ".lease_until")
		if raw, readErr := os.ReadFile(leasePath); readErr == nil {
			if lease, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw))); parseErr == nil && lease.After(time.Now()) {
				continue
			}
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("workspace: remove stale %s: %w", path, err)
		}
	}
	return nil
}

func (w *Workspace) Cleanup() error {
	if err := os.RemoveAll(w.root); err != nil {
		return fmt.Errorf("workspace: cleanup %s: %w", w.root, err)
	}
	return nil
}
