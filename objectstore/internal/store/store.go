// Package store implements a disk-backed object store keyed by content hash.
package store

import (
	"bytes"
	"errors"
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

// PutReader streams an object into a temporary file and atomically installs it.
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
	return os.Rename(tmpPath, p)
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

func (s *Store) path(key string) string {
	prefix := key
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(s.dir, "objects", prefix, key)
}
