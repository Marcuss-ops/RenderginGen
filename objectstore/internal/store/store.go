// Package store implements a disk-backed object store keyed by content hash.
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("object not found")

// Store is a disk-backed object store.
type Store struct {
	dir string
}

// New creates a store rooted at dir.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Put writes object data for key.
func (s *Store) Put(key string, data []byte) error {
	return s.PutReader(key, bytes.NewReader(data), int64(len(data)))
}

// PutReader streams an object into a temporary file, syncs it to stable
// storage and atomically installs it. The HTTP 201 acknowledgement is only
// sent after the rename, and the rename is only made after the file contents
// (and the directory entry) are durable: without the fsyncs a power loss right
// after an acknowledged PUT could silently lose an artifact the worker already
// treated as persisted in L3.
func (s *Store) PutReader(key string, r io.Reader, size int64) error {
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".object-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	written, err := io.Copy(tmp, r)
	if err == nil && size >= 0 && written != size {
		err = errors.New("content length mismatch")
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := syncFile(tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return err
	}
	// Persist the rename itself so the acknowledged object survives a crash.
	dir, err := os.Open(filepath.Dir(p))
	if err != nil {
		return fmt.Errorf("open object directory for sync: %w", err)
	}
	dirErr := dir.Sync()
	dir.Close()
	if dirErr != nil {
		return fmt.Errorf("sync object directory: %w", dirErr)
	}
	return nil
}

// syncFile flushes a file's contents to stable storage before it is exposed
// under its content address.
func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// Get reads object data for key.
func (s *Store) Get(key string) ([]byte, error) {
	data, err := os.ReadFile(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Open returns a streaming reader for the object plus its size (-1 when
// unknown). Callers must Close the reader. Used by the HTTP server so large
// artifacts are streamed from disk instead of buffered in RAM.
func (s *Store) Open(key string) (*os.File, int64, error) {
	p := s.path(key)
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (s *Store) path(key string) string {
	prefix := key
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(s.dir, "objects", prefix, key)
}
