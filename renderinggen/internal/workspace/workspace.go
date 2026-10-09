// Package workspace prepares and cleans a per-job workspace on the worker:
//
//	/var/lib/renderinggen/jobs/<jobID>/
//	├── plan.json           (render_plan written by the caller)
//	├── prepared.json       (immutable overlay package prepared once)
//	├── assets/<logical>    (assets materialized by content hash)
//	└── output/result.mp4   (render output)
//
// MaterializePaths is the single asset resolver/materializer: it pulls each
// asset through the L1/L2/L3 cache (via a path resolver func) and materializes
// it at the logical path declared by the job, so the render_plan's source/asset
// references resolve inside the assets root. Media never round-trips through
// a Go byte slice; the resolver hands back a local file that is streamed into
// an atomically installed file beneath the pinned workspace root.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// ResolvedAsset describes an asset already present on local storage. The
// workspace can link it directly instead of reading it into a Go byte slice.
type ResolvedAsset struct {
	LocalPath string
	SizeBytes int64
}

// PathResolver is the zero-copy asset resolution surface used by production.
type PathResolver func(ctx context.Context, asset queue.AssetRef) (ResolvedAsset, error)

var errWorkspaceBusy = errors.New("workspace: already owned by another worker")

// Workspace is a per-job directory tree prepared for a render.
type Workspace struct {
	root      string
	assetsDir string
	outputDir string
	jobsFS    *os.Root
	fsRoot    *os.Root
	lockFile  *os.File
}

// New creates the job workspace directory tree under jobsRoot.
func New(jobsRoot, jobID string) (*Workspace, error) {
	if err := queueclient.ValidateJobID(jobID); err != nil {
		return nil, fmt.Errorf("workspace: invalid job id: %w", err)
	}
	if err := os.MkdirAll(jobsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("workspace: create jobs root: %w", err)
	}
	canonicalJobsRoot, err := filepath.EvalSymlinks(jobsRoot)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve jobs root: %w", err)
	}
	jobsFS, err := os.OpenRoot(canonicalJobsRoot)
	if err != nil {
		return nil, fmt.Errorf("workspace: open jobs root: %w", err)
	}
	closeJobsRoot := true
	defer func() {
		if closeJobsRoot {
			_ = jobsFS.Close()
		}
	}()
	// The directory entry is created or checked relative to an open jobs-root
	// handle. Opening the per-job root and comparing its file identity with the
	// entry pins subsequent operations to that directory even if the pathname
	// is concurrently renamed or replaced. This and os.Root prevent escaping
	// through symlinks outside the pinned root. Advisory locks coordinate
	// cooperating workers and the stale sweeper; they do not defend against a
	// privileged process that ignores locks and replaces jobsRoot itself.

	if err := jobsFS.Mkdir(jobID, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("workspace: create job root: %w", err)
	}
	lockFile, err := acquireWorkspaceLock(jobsFS, jobID)
	if err != nil {
		return nil, err
	}
	lockOwned := true
	defer func() {
		if lockOwned {
			_ = removeWorkspaceLock(jobsFS, jobID, lockFile, true)
		}
	}()
	entryInfo, err := jobsFS.Lstat(jobID)
	if err != nil {
		return nil, fmt.Errorf("workspace: stat job root: %w", err)
	}
	if !entryInfo.IsDir() || entryInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workspace: job root %q is not a real directory", jobID)
	}
	fsRoot, err := jobsFS.OpenRoot(jobID)
	if err != nil {
		return nil, fmt.Errorf("workspace: open job root: %w", err)
	}
	rootInfo, err := fsRoot.Stat(".")
	if err != nil {
		_ = fsRoot.Close()
		return nil, fmt.Errorf("workspace: stat opened job root: %w", err)
	}
	if !os.SameFile(entryInfo, rootInfo) {
		_ = fsRoot.Close()
		return nil, fmt.Errorf("workspace: job root changed while opening")
	}
	root := filepath.Join(canonicalJobsRoot, jobID)
	w := &Workspace{
		root:      root,
		assetsDir: filepath.Join(root, "assets"),
		outputDir: filepath.Join(root, "output"),
		jobsFS:    jobsFS,
		fsRoot:    fsRoot,
		lockFile:  lockFile,
	}
	for _, dir := range []string{"assets", "output"} {
		if err := fsRoot.MkdirAll(dir, 0o755); err != nil {
			_ = fsRoot.Close()
			return nil, fmt.Errorf("workspace: create %s: %w", dir, err)
		}
	}
	closeJobsRoot = false
	lockOwned = false
	// The per-job output directory must be writable by the native Chronon
	// daemon user when it differs from the worker user. 0o775 + a shared
	// group is the deployment contract; world-writable trees are never
	// created. When the deployment cannot provide a shared group, run the
	// daemon under the same user — do not widen permissions instead.
	return w, nil
}

// acquireWorkspaceLock opens one collision-resistant lock file per job while
// holding the short-lived registry lock. The registry coordinates lock-file
// creation/unlink so a new owner can never lock a newly created inode while a
// previous owner still holds the unlinked inode. The file remains only while
// its workspace is active or until the stale sweeper reaps a crashed owner.
func openWorkspaceLock(root *os.Root, jobID string) (*os.File, error) {
	name := workspaceLockName(jobID)
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrExist) {
		file, err = root.OpenFile(name, os.O_RDWR, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("workspace: open per-job lock: %w", err)
	}
	return file, nil
}

func acquireWorkspaceLock(root *os.Root, jobID string) (*os.File, error) {
	registry, err := acquireLockRegistry(root)
	if err != nil {
		return nil, err
	}
	file, err := openWorkspaceLock(root, jobID)
	if err != nil {
		_ = unlockWorkspaceSlot(registry)
		_ = registry.Close()
		return nil, err
	}
	locked, err := lockWorkspaceSlot(file, true)
	_ = unlockWorkspaceSlot(registry)
	_ = registry.Close()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("workspace: lock per-job lock: %w", err)
	}
	if !locked {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %q", errWorkspaceBusy, jobID)
	}
	return file, nil
}

func acquireLockRegistry(root *os.Root) (*os.File, error) {
	file, err := root.OpenFile(".workspace-lock-registry", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("workspace: open lock registry: %w", err)
	}
	if _, err := lockWorkspaceSlot(file, false); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("workspace: lock registry: %w", err)
	}
	return file, nil
}

func workspaceLockName(jobID string) string {
	digest := sha256.Sum256([]byte(jobID))
	return ".workspace-lock-" + hex.EncodeToString(digest[:])
}

func removeWorkspaceLock(root *os.Root, jobID string, lockFile *os.File, removeEntry bool) error {
	registry, err := acquireLockRegistry(root)
	if err != nil {
		return err
	}
	var removeErr error
	if removeEntry {
		removeErr = root.Remove(workspaceLockName(jobID))
	}
	unlockErr := unlockWorkspaceSlot(lockFile)
	closeLockErr := lockFile.Close()
	registryUnlockErr := unlockWorkspaceSlot(registry)
	registryCloseErr := registry.Close()
	for _, candidate := range []error{removeErr, unlockErr, closeLockErr, registryUnlockErr, registryCloseErr} {
		if candidate != nil && !errors.Is(candidate, os.ErrNotExist) {
			return candidate
		}
	}
	return nil
}

// ensurePathContained requires child to be a strict lexical descendant of root.
func ensurePathContained(root, child string) error {
	rel, err := filepath.Rel(root, child)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("workspace path %q escapes jobs root %q", child, root)
	}
	return nil
}

// rejectSymlinkComponents refuses every existing symlink between root and
// child; it is called before creation so MkdirAll cannot follow one outward.
func rejectSymlinkComponents(root, child string) error {
	if err := ensurePathContained(root, child); err != nil {
		return err
	}
	rel, _ := filepath.Rel(root, child)
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace path component %q is a symlink", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("workspace path component %q is not a directory", current)
		}
	}
	return nil
}

// Root returns the workspace root directory.
func (w *Workspace) Root() string { return w.root }

// assetsRoot returns the directory assets are materialized into.
//
// Unexported on purpose: production code uses Root() — Chronon is invoked with
// the workspace root as its assets root and asset paths are workspace-relative
// (see assetPath) — so this accessor exists for the workspace's own tests. It
// used to be exported, which advertised a second, contradictory definition of
// "assets root" to every consumer of the package.
func (w *Workspace) assetsRoot() string { return w.assetsDir }

// OutputPath returns the path for a rendered output file.
func (w *Workspace) OutputPath(name string) string {
	return filepath.Join(w.outputDir, name)
}

// OpenOutput creates an output file through the pinned workspace root. It
// prevents callers from following a replaced output-directory symlink.
func (w *Workspace) OpenOutput(name string) (*os.File, error) {
	if w == nil || w.fsRoot == nil {
		return nil, errors.New("workspace: output open on closed workspace")
	}
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("workspace: invalid output name %q", name)
	}
	rel := filepath.ToSlash(filepath.Join("output", clean))
	dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
	if dir != "." {
		if err := w.fsRoot.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return w.fsRoot.OpenFile(rel, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
}

// AssetPath returns a validated workspace path for a declared logical asset.
// It is exposed for CPU-only consumers that must inspect materialized inputs
// without writing a Chronon plan or entering the render pipeline.
func (w *Workspace) AssetPath(logical string) (string, error) {
	return w.assetPath(logical)
}

// PlanPath returns the path of the render plan written for Chronon.
func (w *Workspace) PlanPath() string {
	return filepath.Join(w.root, "plan.json")
}

// PreparedPackagePath is the sidecar consumed by prepare/upload integrations.
// Chronon still receives the canonical plan.json; the sidecar keeps the
// content-addressed overlay preparation available without rebuilding it.
func (w *Workspace) PreparedPackagePath() string {
	return filepath.Join(w.root, "prepared.json")
}

// WritePlan writes the render plan document to plan.json through the pinned
// workspace root, so a swapped pathname cannot redirect the write elsewhere.
func (w *Workspace) WritePlan(plan []byte) error {
	if w == nil || w.fsRoot == nil {
		return errors.New("workspace: write plan on closed workspace")
	}
	return w.fsRoot.WriteFile("plan.json", plan, 0o644)
}

// WritePreparedPackage writes the immutable overlay preparation sidecar through
// the pinned workspace root.
func (w *Workspace) WritePreparedPackage(pkg []byte) error {
	if w == nil || w.fsRoot == nil {
		return errors.New("workspace: write prepared package on closed workspace")
	}
	return w.fsRoot.WriteFile("prepared.json", pkg, 0o644)
}

// MaterializePaths resolves every asset to a local file and hard-links it into
// the workspace. A streaming copy is used only when the cache and workspace
// are on different filesystems.
func (w *Workspace) MaterializePaths(ctx context.Context, resolve PathResolver, assets []queue.AssetRef) error {
	if w == nil || w.fsRoot == nil {
		return errors.New("workspace: materialize on closed workspace")
	}
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
	rel, err := filepath.Rel(w.root, dst)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workspace: materialized path %q is outside job root", dst)
	}
	rel = filepath.ToSlash(rel)
	dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
	if dir != "." {
		if err := w.fsRoot.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("workspace: mkdir %s: %w", dir, err)
		}
	}
	if resolved.LocalPath == dst {
		return nil
	}
	// Rooted temp creation and rename prevent a swapped assets directory or
	// destination symlink from redirecting materialization outside the job.
	if err := copyFileRooted(ctx, w.fsRoot, resolved.LocalPath, rel); err != nil {
		return err
	}
	return nil
}

// copyFileRooted streams from the external cache into an exclusive temporary
// file beneath the pinned workspace root, then atomically installs it.
func copyFileRooted(ctx context.Context, root *os.Root, source, dst string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("workspace: open source %s: %w", source, err)
	}
	defer input.Close()
	dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(dst)))
	if dir == "." {
		dir = ""
	}
	name := ".materialize-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if dir != "" {
		name = dir + "/" + name
	}
	tmp, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("workspace: create rooted temp: %w", err)
	}
	defer root.Remove(name)
	buf := copyBufPool.Get().(*[]byte)
	_, copyErr := io.CopyBuffer(tmp, input, *buf)
	copyBufPool.Put(buf)
	if copyErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("workspace: copy %s: %w", dst, copyErr)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("workspace: close temp: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := root.Rename(name, dst); err != nil {
		return fmt.Errorf("workspace: install %s: %w", dst, err)
	}
	return nil
}

// copyBufPool recycles the streaming materialization buffer. It is larger
// than io.Copy's built-in 32 KiB because these are video-sized assets.
var copyBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 256*1024)
		return &buf
	},
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
	var candidate string
	if clean == "assets" || strings.HasPrefix(clean, "assets"+string(filepath.Separator)) {
		candidate = filepath.Join(w.root, clean)
	} else {
		candidate = filepath.Join(w.assetsDir, clean)
	}
	if err := ensurePathContained(w.root, candidate); err != nil {
		return "", fmt.Errorf("workspace: logical_path %q escapes the workspace", logical)
	}
	return candidate, nil
}

// CleanupStale removes old workspace directories. A valid lease marker protects
// active work; a process lock independently protects live owners even if the
// marker is missing, stale, or being refreshed. Lock entries are removed only
// while the global lock registry and the per-job exclusive lock are both held.
// These advisory locks require cooperating processes and a filesystem that
// implements Linux flock semantics; do not share jobsRoot over a filesystem
// with different lock guarantees.
func CleanupStale(root string, olderThan time.Duration) error {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rootFS, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return err
	}
	defer rootFS.Close()
	entries, err := fs.ReadDir(rootFS.FS(), ".")
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-olderThan)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			// Dot-directories are worker bookkeeping, never job workspaces: the
			// jobs root holds `.workerlogs` (the worker's durable per-job log
			// files). Sweeping it would delete the only record of a run that does
			// not depend on journald, and it is not a leaked scratch tree.
			continue
		}
		path := filepath.Join(canonicalRoot, entry.Name())
		info, err := rootFS.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.ModTime().After(cutoff) {
			continue
		}
		jobFS, openErr := rootFS.OpenRoot(entry.Name())
		if openErr != nil {
			// The candidate may have been replaced after Lstat. Never follow it
			// through its pathname; safely skip entries that are no longer dirs.
			if errors.Is(openErr, os.ErrNotExist) || errors.Is(openErr, syscall.ENOTDIR) {
				continue
			}
			return fmt.Errorf("workspace: open stale candidate: %w", openErr)
		}
		registry, registryErr := acquireLockRegistry(rootFS)
		if registryErr != nil {
			_ = jobFS.Close()
			return registryErr
		}
		lockFile, lockErr := openWorkspaceLock(rootFS, entry.Name())
		if lockErr != nil {
			_ = unlockWorkspaceSlot(registry)
			_ = registry.Close()
			_ = jobFS.Close()
			return fmt.Errorf("workspace: open stale candidate lock: %w", lockErr)
		}
		locked, lockErr := lockWorkspaceSlot(lockFile, true)
		if lockErr != nil {
			_ = lockFile.Close()
			_ = unlockWorkspaceSlot(registry)
			_ = registry.Close()
			_ = jobFS.Close()
			return fmt.Errorf("workspace: lock stale candidate: %w", lockErr)
		}
		if !locked {
			_ = lockFile.Close()
			_ = unlockWorkspaceSlot(registry)
			_ = registry.Close()
			_ = jobFS.Close()
			continue
		}
		leaseRaw, leaseReadErr := jobFS.ReadFile(leaseMarkerName)
		_ = jobFS.Close()
		if leaseReadErr == nil {
			if lease, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(leaseRaw))); parseErr == nil && lease.After(time.Now()) {
				_ = unlockWorkspaceSlot(lockFile)
				_ = lockFile.Close()
				_ = unlockWorkspaceSlot(registry)
				_ = registry.Close()
				continue
			}
		}
		if err := rootFS.RemoveAll(entry.Name()); err != nil {
			_ = unlockWorkspaceSlot(lockFile)
			_ = lockFile.Close()
			_ = unlockWorkspaceSlot(registry)
			_ = registry.Close()
			return fmt.Errorf("workspace: remove stale %s: %w", path, err)
		}
		if err := rootFS.Remove(workspaceLockName(entry.Name())); err != nil && !os.IsNotExist(err) {
			_ = unlockWorkspaceSlot(lockFile)
			_ = lockFile.Close()
			_ = unlockWorkspaceSlot(registry)
			_ = registry.Close()
			return fmt.Errorf("workspace: remove stale lock for %s: %w", path, err)
		}
		_ = unlockWorkspaceSlot(lockFile)
		_ = lockFile.Close()
		_ = unlockWorkspaceSlot(registry)
		_ = registry.Close()
	}
	return nil
}

// Cleanup removes the workspace directory tree while holding its per-job lock.
func (w *Workspace) Cleanup() error {
	if w == nil {
		return nil
	}
	if w.fsRoot == nil && w.jobsFS == nil && w.lockFile == nil {
		return nil
	}
	var cleanupErr error
	if w.jobsFS != nil && w.lockFile != nil {
		registry, err := acquireLockRegistry(w.jobsFS)
		if err != nil {
			cleanupErr = err
		} else {
			if err := w.jobsFS.RemoveAll(filepath.Base(w.root)); err != nil && !os.IsNotExist(err) {
				cleanupErr = fmt.Errorf("workspace: cleanup %s: %w", w.root, err)
			}
			if cleanupErr == nil {
				if err := w.jobsFS.Remove(workspaceLockName(filepath.Base(w.root))); err != nil && !os.IsNotExist(err) {
					cleanupErr = fmt.Errorf("workspace: remove per-job lock entry: %w", err)
				}
			}
			_ = unlockWorkspaceSlot(registry)
			_ = registry.Close()
		}

	} else if w.jobsFS != nil {
		if err := w.jobsFS.RemoveAll(filepath.Base(w.root)); err != nil && !os.IsNotExist(err) {
			cleanupErr = fmt.Errorf("workspace: cleanup %s: %w", w.root, err)
		}
	}
	if w.fsRoot != nil {
		if err := w.fsRoot.Close(); err != nil && cleanupErr == nil {
			cleanupErr = fmt.Errorf("workspace: close workspace root: %w", err)
		}
		w.fsRoot = nil
	}
	if w.lockFile != nil {
		// Keep a failed cleanup's per-job lock held: otherwise a retry would
		// reopen the surviving directory without the original ownership guard.
		if cleanupErr == nil {
			if err := unlockWorkspaceSlot(w.lockFile); err != nil {
				cleanupErr = fmt.Errorf("workspace: unlock workspace: %w", err)
			} else if err := w.lockFile.Close(); err != nil {
				cleanupErr = fmt.Errorf("workspace: close workspace lock: %w", err)
			} else {
				w.lockFile = nil
			}
		}
	}
	if w.jobsFS != nil {
		if err := w.jobsFS.Close(); err != nil && cleanupErr == nil {
			cleanupErr = fmt.Errorf("workspace: close jobs root: %w", err)
		}
		w.jobsFS = nil
	}
	return cleanupErr
}

// leaseMarkerName is the per-workspace liveness marker CleanupStale reads.
// A workspace whose marker is still valid is never swept, regardless of how
// old its directory mtime is. The worker writes it when a job is prepared
// and refreshes it while the render runs (a running render may legitimately
// write nothing to the workspace for more than an hour).
const leaseMarkerName = ".lease_until"

// WriteLease writes/updates the workspace liveness marker with the given
// expiry. It complements, but does not replace, the per-job advisory process lock.
func (w *Workspace) WriteLease(until time.Time) error {
	if w == nil {
		return nil
	}
	if w.fsRoot == nil {
		return errors.New("workspace: lease on closed workspace")
	}
	if err := w.fsRoot.WriteFile(leaseMarkerName, []byte(until.UTC().Format(time.RFC3339Nano)), 0o644); err != nil {
		return fmt.Errorf("workspace: write lease marker %s: %w", w.root, err)
	}
	return nil
}
