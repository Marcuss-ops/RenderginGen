// Package workspace prepares and cleans a per-job workspace on the worker:
//
//	/var/lib/renderinggen/jobs/<jobID>/
//	├── plan.json           (render_plan written by the caller)
//	├── assets/<logical>    (assets materialized by content hash)
//	└── output/result.mp4   (render output)
//
// MaterializePaths is the single asset resolver/materializer: it pulls each
// asset through the L1/L2/L3 cache (via a path resolver func) and links it to
// the logical path declared by the job, so the render_plan's source/asset
// references resolve inside the assets root. Media never round-trips through
// a Go byte slice; the resolver hands back a local file the workspace can
// hard-link (or stream-copy across filesystems).
package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// ResolvedAsset describes an asset already present on local storage. The
// workspace can link it directly instead of reading it into a Go byte slice.
type ResolvedAsset struct {
	LocalPath string
	SizeBytes int64
}

// PathResolver is the zero-copy asset resolution surface used by production.
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
	// The per-job output directory must be writable by the native Chronon
	// daemon user when it differs from the worker user. 0o775 + a shared
	// group is the deployment contract; world-writable trees are never
	// created. When the deployment cannot provide a shared group, run the
	// daemon under the same user — do not widen permissions instead.
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

// MaterializePaths resolves every asset to a local file and hard-links it into
// the workspace. A streaming copy is used only when the cache and workspace
// are on different filesystems.
func (w *Workspace) MaterializePaths(ctx context.Context, resolve PathResolver, assets []queue.AssetRef) error {
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
				if err := w.materializeOne(workCtx, resolve, a); err != nil {
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

func (w *Workspace) materializeOne(ctx context.Context, resolve PathResolver, a queue.AssetRef) error {
	dst, err := w.assetPath(a.LogicalPath)
	if err != nil {
		return err
	}
	resolved, err := resolve(ctx, a)
	if err != nil {
		return fmt.Errorf("workspace: resolve %s: %w", a.Hash, err)
	}
	if resolved.LocalPath == "" {
		return fmt.Errorf("workspace: resolver returned an empty local path for %s", a.Hash)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if resolved.LocalPath != dst {
		// Idempotent materialization: an existing destination from a previous
		// attempt is replaced atomically (link to a temp sibling + rename), so
		// a retried job never sees a half-installed asset.
		if _, statErr := os.Lstat(dst); statErr == nil {
			_ = os.Remove(dst)
		}
		if err := os.Link(resolved.LocalPath, dst); err != nil {
			if isCrossDevice(err) {
				// Source and destination live on different filesystems: fall
				// back to a streaming copy into a temp file + atomic rename.
				if copyErr := copyFile(ctx, resolved.LocalPath, dst); copyErr != nil {
					return copyErr
				}
			} else if !os.IsExist(err) {
				return fmt.Errorf("workspace: link %s: %w", dst, err)
			}
		}
	}
	// The worker and the native Chronon daemon may run as different users.
	// Temp/cache files are commonly created as 0600; make the immutable
	// materialized view readable by the daemon without making it writable.
	if err := os.Chmod(dst, 0o644); err != nil {
		return fmt.Errorf("workspace: chmod %s: %w", dst, err)
	}
	return nil
}

// isCrossDevice is the single expression of the cross-filesystem decision
// (link fails with EXDEV when the cache and the workspace live on different
// mounts). Every materialization path funnels through it so a future change
// to the fallback policy is made in exactly one place.
func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}

func copyFile(ctx context.Context, source, dst string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("workspace: open %s: %w", source, err)
	}
	defer input.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".materialize-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, input); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("workspace: install %s: %w", dst, err)
	}
	if err := os.Chmod(dst, 0o644); err != nil {
		return fmt.Errorf("workspace: chmod %s: %w", dst, err)
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
	// Concrete Chronon plans use the canonical `assets/...` namespace and are
	// rendered with the workspace root as Chronon's assets root. Legacy queue
	// refs such as `videos/base.mp4` are still relative to the workspace's
	// assets directory. Supporting both here keeps one materialization rule at
	// the RenderingGen boundary.
	if clean == "assets" || strings.HasPrefix(clean, "assets"+string(filepath.Separator)) {
		return filepath.Join(w.root, clean), nil
	}
	return filepath.Join(w.assetsDir, clean), nil
}

// CleanupStale removes old workspace directories. A workspace is considered
// active when it contains a lease marker whose timestamp is still valid (see
// WriteLease); any other directory older than olderThan is swept. Workers
// write/refresh the marker for the whole lifetime of a claimed job, so a
// long render that writes nothing to its workspace can never be swept.
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
		leasePath := filepath.Join(path, leaseMarkerName)
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

// Cleanup removes the workspace directory tree.
func (w *Workspace) Cleanup() error {
	if err := os.RemoveAll(w.root); err != nil {
		return fmt.Errorf("workspace: cleanup %s: %w", w.root, err)
	}
	return nil
}

// leaseMarkerName is the per-workspace liveness marker CleanupStale reads.
// A workspace whose marker is still valid is never swept, regardless of how
// old its directory mtime is. The worker writes it when a job is prepared
// and refreshes it while the render runs (a running render may legitimately
// write nothing to the workspace for more than an hour).
const leaseMarkerName = ".lease_until"

// WriteLease writes/updates the workspace liveness marker with the given
// expiry. Until that instant, CleanupStale must never remove this workspace.
func (w *Workspace) WriteLease(until time.Time) error {
	if w == nil {
		return nil
	}
	if err := os.WriteFile(filepath.Join(w.root, leaseMarkerName), []byte(until.UTC().Format(time.RFC3339Nano)), 0o644); err != nil {
		return fmt.Errorf("workspace: write lease marker %s: %w", w.root, err)
	}
	return nil
}
