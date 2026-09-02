package drive

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Mock is an in-process Publisher for tests and the local e2e smoke. It writes
// uploaded bytes to a directory and can fail the first N uploads, which lets a
// test exercise the publication-retry path deterministically (first upload
// fails, retry succeeds) without a real Google Drive account.
type Mock struct {
	mu        sync.Mutex
	dir       string
	failFirst int
	failures  int
	seq       int
}

// NewMock creates a Mock publisher. When dir is non-empty, successful uploads
// are written there as %04d-<name>. failFirst is the number of uploads to fail
// before succeeding.
func NewMock(dir string, failFirst int) *Mock {
	return &Mock{dir: dir, failFirst: failFirst}
}

// Publish fails the first failFirst calls, then writes the artifact to dir and
// returns a deterministic file ID and link.
func (m *Mock) Publish(_ context.Context, req PublishRequest) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seq++
	if m.failures < m.failFirst {
		m.failures++
		return Result{}, fmt.Errorf("drive: simulated upload failure %d/%d", m.failures, m.failFirst)
	}

	if req.Path == "" {
		return Result{}, fmt.Errorf("drive: mock publish has empty path")
	}
	input, err := os.Open(req.Path)
	if err != nil {
		return Result{}, fmt.Errorf("drive: mock open: %w", err)
	}
	defer input.Close()
	data, err := io.ReadAll(input)
	if err != nil {
		return Result{}, fmt.Errorf("drive: mock read: %w", err)
	}
	if m.dir != "" {
		dir := m.dir
		if req.Subfolder != "" {
			dir = filepath.Join(dir, req.Subfolder)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{}, fmt.Errorf("drive: mock mkdir: %w", err)
		}
		name := fmt.Sprintf("%04d-%s", m.seq, filepath.Base(req.Name))
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return Result{}, fmt.Errorf("drive: mock write: %w", err)
		}
	}

	id := fmt.Sprintf("mock-%04d", m.seq)
	hash := sha256.Sum256(data)
	return Result{FileID: id, WebViewLink: "https://drive.example.com/file/d/" + id,
		ParentFolder: req.ParentFolder, SizeBytes: int64(len(data)), SHA256: fmt.Sprintf("%x", hash[:])}, nil
}
