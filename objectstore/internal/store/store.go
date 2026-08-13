// Package store implements a disk-backed object store keyed by content hash.
package store

import (
	"errors"
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
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
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
